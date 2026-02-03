package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Global storage for the VCS (Problem requires shared storage, not temp per session)
// Map: Filename -> Slice of Revisions (each revision is []byte)
var (
	fileStore = make(map[string][][]byte)
	// Allow only: a-z, A-Z, 0-9, forward slash (/), dot (.), underscore (_), hyphen (-)
	validPath  = regexp.MustCompile(`^[a-zA-Z0-9/._-]+$`)
	storeMutex sync.RWMutex
)

func main() {
	// Idiomatic Go argument parsing
	hostname := flag.String("h", "0.0.0.0", "Hostname to bind")
	port := flag.String("p", "2001", "Port to bind")
	flag.Parse()

	addr := fmt.Sprintf("%s:%s", *hostname, *port)
	fmt.Printf("Starting VCS Server on %s...\n", addr)

	runServer(addr)
}

func runServer(addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Connection error:", err)
			continue
		}
		// Handle each connection in a new goroutine
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	// WRAPPER: This is the key. We wrap the connection in a Reader.
	// We read both lines and raw bytes from this SAME reader.
	reader := bufio.NewReader(conn)

	// Send initial greeting
	conn.Write([]byte("READY\n"))

	for {
		// 1. TEXT PHASE: Read the command line
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Println("Read error:", err)
			}
			return
		}

		log.Printf("RECEIVED: %q", line)

		// Trim whitespace and split command
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)

		if len(parts) == 0 {
			continue // Handle empty lines gracefully if needed
		}

		cmd := strings.ToUpper(parts[0])

		switch cmd {
		case "PUT":
			// PUT /file.txt 123
			if len(parts) != 3 {
				conn.Write([]byte("ERR usage: PUT file length\nREADY\n"))
				continue
			}

			filename := parts[1]

			// Rule 1: Must be absolute path (start with /)
			if !strings.HasPrefix(filename, "/") {
				conn.Write([]byte("ERR illegal file name\nREADY\n"))
				continue
			}

			// Rule 2: Must match allowed characters
			if !validPath.MatchString(filename) {
				conn.Write([]byte("ERR illegal file name\nREADY\n"))
				continue
			}

			storeKey := strings.ToLower(filename)
			length, err := strconv.Atoi(parts[2])
			if err != nil {
				conn.Write([]byte("ERR invalid length\nREADY\n"))
				continue
			}

			fileData := make([]byte, length)
			_, err = io.ReadFull(reader, fileData)
			if err != nil {
				log.Println("Error reading file body:", err)
				return
			}

			// Check 1: Is it valid UTF-8?
			if !utf8.Valid(fileData) {
				conn.Write([]byte("ERR text files only\nREADY\n"))
				continue
			}

			// Check 2: Does it contain non-printable control characters? (Optional but likely required)
			// We allow \n (10), \r (13), \t (9). We disallow NULL (0) or other controls.
			isText := true
			for _, b := range fileData {
				if b < 32 && b != 10 && b != 13 && b != 9 {
					isText = false
					break
				}
				// Also reject the Delete char (127) if you want to be strict
				if b == 127 {
					isText = false
					break
				}
			}

			if !isText {
				conn.Write([]byte("ERR text files only\nREADY\n"))
				continue
			}

			// Store the file (Thread-safe)
			storeMutex.Lock()

			// Get existing revisions for this file
			currentRevisions := fileStore[storeKey]
			var revID int

			// CHECK FOR DUPLICATE CONTENT
			// If the file exists and the new data matches the last revision...
			if len(currentRevisions) > 0 && bytes.Equal(currentRevisions[len(currentRevisions)-1], fileData) {
				// ... we do NOT create a new revision.
				revID = len(currentRevisions)
				log.Printf("Skipping duplicate revision for %s (keeping r%d)\n", filename, revID)
			} else {
				// Otherwise, append as a new revision
				fileStore[storeKey] = append(fileStore[storeKey], fileData)
				revID = len(fileStore[storeKey])
				log.Printf("Stored new revision for %s (r%d)\n", filename, revID)
			}

			storeMutex.Unlock()

			// Send confirmation
			response := fmt.Sprintf("OK r%d\nREADY\n", revID)
			conn.Write([]byte(response))

		case "GET":
			// GET /file.txt [r#]
			if len(parts) < 2 {
				conn.Write([]byte("ERR usage: GET file [revision]\nREADY\n"))
				continue
			}

			filename := parts[1]
			storeKey := strings.ToLower(filename)

			// Read-Lock the storage to find the file
			storeMutex.RLock()
			revisions, exists := fileStore[storeKey]

			// Case: File does not exist
			if !exists {
				storeMutex.RUnlock()
				conn.Write([]byte("ERR no such file\nREADY\n"))
				continue
			}

			// Determine which revision index to fetch
			var targetIndex int

			if len(parts) >= 3 {
				// User requested specific revision (e.g., "r1")
				revString := parts[2]

				// Validate format (must start with 'r')
				if !strings.HasPrefix(strings.ToLower(revString), "r") {
					storeMutex.RUnlock()
					conn.Write([]byte("ERR no such revision\nREADY\n"))
					continue
				}

				// Parse the number
				revNum, err := strconv.Atoi(revString[1:]) // strip 'r'
				if err != nil {
					storeMutex.RUnlock()
					conn.Write([]byte("ERR no such revision\nREADY\n"))
					continue
				}

				// Convert 1-based revision ID to 0-based slice index
				targetIndex = revNum - 1
			} else {
				// Default: Get the latest revision (last item in slice)
				targetIndex = len(revisions) - 1
			}

			// Bounds Check
			if targetIndex < 0 || targetIndex >= len(revisions) {
				storeMutex.RUnlock()
				conn.Write([]byte("ERR no such revision\nREADY\n"))
				continue
			}

			// Get the data reference
			fileData := revisions[targetIndex]

			// We can unlock now because we have the reference to the byte slice
			// (Assuming existing revisions are never modified/deleted in this VCS)
			storeMutex.RUnlock()

			// Send Response
			// 1. Header: OK <length>
			header := fmt.Sprintf("OK %d\n", len(fileData))
			conn.Write([]byte(header))

			// 2. Body: Raw bytes (No newline after!)
			conn.Write(fileData)

			// 3. Footer: READY
			conn.Write([]byte("READY\n"))

		case "LIST":
			// Request: LIST <dir>
			if len(parts) != 2 {
				conn.Write([]byte("ERR usage: LIST dir\nREADY\n"))
				continue
			}

			// Normalize target: lowercase, and ensure it ends with "/"
			// This makes the prefix stripping logic much safer.
			targetDir := strings.ToLower(parts[1])
			if !strings.HasSuffix(targetDir, "/") {
				targetDir += "/"
			}

			storeMutex.RLock()

			// Use a map to deduplicate directory entries
			// Key = Display Name (e.g. "folder/"), Value = Metadata (e.g. "DIR" or "r1")
			entries := make(map[string]string)

			for path, revisions := range fileStore {
				// path is already lowercase because we stored it that way

				// 1. Check if file is inside this directory
				if strings.HasPrefix(path, targetDir) {

					// 2. Get the relative path
					// e.g. target="/", path="/dir/file.txt" -> relative="dir/file.txt"
					relative := strings.TrimPrefix(path, targetDir)

					// 3. Check for subdirectories
					if idx := strings.Index(relative, "/"); idx != -1 {
						// It is in a subdir!
						// relative="dir/file.txt" -> dirName="dir"
						dirName := relative[:idx]

						// Add the slash to match the probe output "probe_folder/"
						entries[dirName+"/"] = "DIR"
					} else {
						// It is a direct file in this directory
						// relative="file.txt"
						rev := len(revisions)
						entries[relative] = fmt.Sprintf("r%d", rev)
					}
				}
			}
			storeMutex.RUnlock()

			// 4. Sort the keys (Critical for tests)
			var sortedNames []string
			for name := range entries {
				sortedNames = append(sortedNames, name)
			}
			sort.Strings(sortedNames)

			// 5. Response
			conn.Write([]byte(fmt.Sprintf("OK %d\n", len(sortedNames))))

			for _, name := range sortedNames {
				meta := entries[name]
				// Format: "name metadata"
				// e.g. "folder/ DIR" or "file.txt r1"
				conn.Write([]byte(fmt.Sprintf("%s %s\n", name, meta)))
			}
			conn.Write([]byte("READY\n"))

		case "HELP":
			conn.Write([]byte("OK usage: HELP|GET|PUT|LIST\nREADY\n"))

		default:
			conn.Write([]byte("ERR illegal method: " + cmd + "\nREADY\n"))
		}
	}
}
