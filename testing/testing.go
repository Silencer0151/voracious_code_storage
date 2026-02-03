package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Config
var (
	targetHost string
	targetPort string
)

func main() {
	flag.StringVar(&targetHost, "h", "vcs.protohackers.com", "Target Host")
	flag.StringVar(&targetPort, "p", "30307", "Target Port")
	flag.Parse()

	addr := fmt.Sprintf("%s:%s", targetHost, targetPort)
	fmt.Printf("=== Starting Test Suite against %s ===\n", addr)

	// Run Tests
	runTest(addr, "Basic PUT and GET", testBasicPutGet)
	runTest(addr, "Revision Incrementing", testRevisions)
	runTest(addr, "Directory Listing", testListing)
	runTest(addr, "Binary Data Safety", testBinaryData)
}

// Wrapper to handle connection setup/teardown for each test
func runTest(addr, name string, testFunc func(*bufio.Reader, net.Conn) error) {
	fmt.Printf("TEST: %-25s ... ", name)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Printf("FAIL (Connection): %v\n", err)
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// Consume initial READY
	if _, err := readUntil(reader, "READY"); err != nil {
		fmt.Printf("FAIL (Init): %v\n", err)
		return
	}

	if err := testFunc(reader, conn); err != nil {
		fmt.Printf("FAIL: %v\n", err)
	} else {
		fmt.Println("PASS")
	}
}

// --- Actual Tests ---

func testBasicPutGet(r *bufio.Reader, w net.Conn) error {
	filename := "/sanity.txt"
	data := "Testing123"

	// 1. Upload
	if err := sendPut(r, w, filename, []byte(data)); err != nil {
		return err
	}

	// 2. Download
	received, err := sendGet(r, w, filename, "")
	if err != nil {
		return err
	}

	if string(received) != data {
		return fmt.Errorf("content mismatch: expected '%s', got '%s'", data, received)
	}
	return nil
}

func testRevisions(r *bufio.Reader, w net.Conn) error {
	filename := "/rev_test.txt"
	v1 := "Version_One"
	v2 := "Version_Two"

	// Upload v1
	if err := sendPut(r, w, filename, []byte(v1)); err != nil {
		return err
	}

	// Upload v2 (Should be r2)
	// Note: We don't explicitly parse "OK r2" here for simplicity,
	// but we verify the data matches r2.
	if err := sendPut(r, w, filename, []byte(v2)); err != nil {
		return err
	}

	// Get latest (Should be v2)
	gotV2, err := sendGet(r, w, filename, "")
	if err != nil {
		return err
	}
	if string(gotV2) != v2 {
		return fmt.Errorf("latest wasn't v2")
	}

	// Get r1 explicitly (Should be v1)
	gotV1, err := sendGet(r, w, filename, "r1")
	if err != nil {
		return err
	}
	if string(gotV1) != v1 {
		return fmt.Errorf("r1 request didn't return v1")
	}

	return nil
}

func testListing(r *bufio.Reader, w net.Conn) error {
	// Upload two files
	sendPut(r, w, "/dir/a.txt", []byte("A"))
	sendPut(r, w, "/dir/b.txt", []byte("B"))

	// List /dir
	fmt.Fprintf(w, "LIST /dir\n")

	// Expect "OK <count>"
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}

	if !strings.HasPrefix(line, "OK") {
		return fmt.Errorf("expected OK, got %s", strings.TrimSpace(line))
	}

	// We expect at least 2 lines (could be more if shared server)
	// Just read until READY
	foundA := false
	foundB := false

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)

		if line == "READY" {
			break
		}

		// Check for our files
		if strings.Contains(line, "a.txt") {
			foundA = true
		}
		if strings.Contains(line, "b.txt") {
			foundB = true
		}
	}

	if !foundA || !foundB {
		return fmt.Errorf("listing missing uploaded files")
	}
	return nil
}

func testBinaryData(r *bufio.Reader, w net.Conn) error {
	// Create data with null bytes and invalid UTF-8
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0xFF}
	filename := "/bin.dat"

	// Send the PUT command manually so we can check the specific error
	cmd := fmt.Sprintf("PUT %s %d\n", filename, len(data))
	w.Write([]byte(cmd))
	w.Write(data) // Send raw binary

	// Read response
	resp, err := r.ReadString('\n')
	if err != nil {
		return err
	}

	resp = strings.TrimSpace(resp)

	// WE EXPECT AN ERROR NOW!
	if !strings.HasPrefix(resp, "ERR text files only") {
		return fmt.Errorf("server accepted binary data but should have rejected it. Got: %s", resp)
	}

	// Consume the READY prompt
	readUntil(r, "READY")

	return nil
}

// --- Protocol Helpers ---

func sendPut(r *bufio.Reader, w net.Conn, filename string, data []byte) error {
	// Send Command Line
	cmd := fmt.Sprintf("PUT %s %d\n", filename, len(data))
	w.Write([]byte(cmd))

	// Send Raw Bytes (NO NEWLINE AFTER)
	w.Write(data)

	// Check Response (Expect "OK rX")
	resp, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(resp, "OK") {
		return fmt.Errorf("PUT failed: %s", strings.TrimSpace(resp))
	}

	// Consume READY
	readUntil(r, "READY")
	return nil
}

func sendGet(r *bufio.Reader, w net.Conn, filename, revision string) ([]byte, error) {
	cmd := fmt.Sprintf("GET %s %s\n", filename, revision)
	w.Write([]byte(cmd))

	// Expect "OK <length>"
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "OK") {
		return nil, fmt.Errorf("GET error: %s", strings.TrimSpace(line))
	}

	// Parse Length
	var length int
	fmt.Sscanf(strings.TrimSpace(line), "OK %d", &length)

	// Read Bytes
	data := make([]byte, length)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return nil, err
	}

	// Consume READY
	readUntil(r, "READY")

	return data, nil
}

func readUntil(r *bufio.Reader, target string) (string, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(line) == target {
			return line, nil
		}
	}
}
