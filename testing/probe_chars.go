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
		filename := fmt.Sprintf("/test_%c_file", char)

		// Skip slash as it is a directory separator
		if char == '/' {
			allowed = append(allowed, "/")
			continue
		}

		fmt.Printf("Testing '%c' ... ", char)

		// Send PUT command
		payload := fmt.Sprintf("PUT %s 1\nA", filename)
		fmt.Fprintf(conn, "%s", payload)

		// Read Response (Expected: OK... or ERR...)
		resp, err := readLine(reader)
		if err != nil {
			fmt.Println("Connection Error:", err)
			break
		}

		if strings.HasPrefix(resp, "OK") {
			fmt.Println("ALLOWED")
			allowed = append(allowed, string(char))
			// Success always sends READY, consume it blocking
			readLine(reader)
		} else {
			fmt.Println("ILLEGAL")
			illegal = append(illegal, string(char))

			// ERROR handling: The server might NOT send READY.
			// Try to read it with a very short timeout.
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			line, err := readLine(reader)
			conn.SetReadDeadline(time.Time{}) // Reset to no timeout

			if err == nil && line != "READY" {
				// We read something that wasn't READY? Odd, but we consumed it.
			}
		}

		time.Sleep(50 * time.Millisecond)
	}

	fmt.Println("\n--- RESULTS ---")
	fmt.Printf("ALLOWED: %s\n", strings.Join(allowed, " "))
	fmt.Printf("ILLEGAL: %s\n", strings.Join(illegal, ""))

	// Print regex suggestion
	regexStr := "^[a-zA-Z0-9"
	for _, c := range allowed {
		if c == "-" || c == "." || c == "/" {
			regexStr += c
		} else {
			regexStr += "\\" + c
		}
	}
	regexStr += "]+$"

	fmt.Printf("Suggested Regex: %s\n", regexStr)
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
