package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
)

func main() {
	conn, err := net.Dial("tcp", "vcs.protohackers.com:30307")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	// Consume initial READY
	readUntil(reader, "READY")

	fmt.Println("--- TEST 1: Uploading nested file ---")
	// Upload /probe_folder/test.txt
	payload := "PUT /probe_folder/test.txt 5\nhello"
	fmt.Fprintf(conn, "%s", payload)

	// Read response (OK r1... READY)
	resp, _ := readUntil(reader, "READY")
	fmt.Printf("Upload response: %q\n", resp)

	fmt.Println("\n--- TEST 2: Listing Root (/) ---")
	fmt.Fprintf(conn, "LIST /\n")

	// Read until READY, printing every line
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "READY" {
			break
		}
		fmt.Printf("LIST ENTRY: %s\n", line)
	}
}

func readUntil(r *bufio.Reader, target string) (string, error) {
	var fullOutput strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return fullOutput.String(), err
		}
		fullOutput.WriteString(line)
		if strings.TrimSpace(line) == target {
			return fullOutput.String(), nil
		}
	}
}
