package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

func main() {
	// All printable non-alphanumeric ASCII characters
	specialChars := "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

	host := "vcs.protohackers.com:30307"
	fmt.Printf("Probing %s for illegal filename characters...\n", host)

	conn, err := net.Dial("tcp", host)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Consume initial READY
	readLine(reader)

	var allowed []string
	var illegal []string

	for _, char := range specialChars {
		// Construct a filename using the special char
		// We wrap it in standard chars to ensure the char itself is the issue
		// e.g. /test_@_file
		filename := fmt.Sprintf("/test_%c_file", char)

		// If char is '/', valid path structure changes, handle separately or just ignore for now
		// (We know / is allowed as a separator)
		if char == '/' {
			allowed = append(allowed, "/")
			continue
		}

		fmt.Printf("Testing '%c' ... ", char)

		// Send PUT command
		// PUT /test_X_file 1\nA
		payload := fmt.Sprintf("PUT %s 1\nA", filename)
		fmt.Fprintf(conn, "%s", payload)

		// Read Response
		resp, _ := readLine(reader)

		if strings.HasPrefix(resp, "OK") {
			fmt.Println("ALLOWED")
			allowed = append(allowed, string(char))
			// Consume the 'READY' that follows OK
			readLine(reader)
		} else {
			fmt.Println("ILLEGAL")
			illegal = append(illegal, string(char))
			// Consume the 'READY' that follows ERR (if server sends it on err)
			// Based on previous logs, server sends READY after ERR too.
			readLine(reader)
		}

		// Small sleep to be polite to the shared server
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Println("\n--- RESULTS ---")
	fmt.Printf("ALLOWED: %s\n", strings.Join(allowed, " "))
	fmt.Printf("ILLEGAL: %s\n", strings.Join(illegal, ""))

	// Print regex suggestion
	escapedAllowed := ""
	for _, c := range allowed {
		if c == "-" || c == "." || c == "/" {
			// these often need escaping or special placement in regex
			escapedAllowed += c
		} else {
			escapedAllowed += "\\" + c
		}
	}
	fmt.Printf("Suggested Regex Safe Chars: a-zA-Z0-9%s\n", strings.Join(allowed, ""))
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
