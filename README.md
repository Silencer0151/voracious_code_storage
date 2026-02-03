# Voracious Code Storage (VCS) - Project Summary

## 1. Overview

Implemented a thread-safe, persistent Version Control System (VCS) server in Go. The protocol was reverse-engineered from a "black box" reference server using manual probing (netcat) and automated fuzzing. The final server supports concurrent clients, handles mixed text/binary payloads, and mimics a hierarchical file system.

## 2. Key Learnings & Challenges

- **Mixed Protocols:** Handling the transition between line-based commands (ASCII) and fixed-length payloads (Binary) requires precise buffering (`bufio.Reader`).

- **The "Ghost" Newline:** Manual probing with netcat often inserts invisible newlines that break strict protocols; learned to use `printf` for exact control.

- **Implicit Requirements:**
  - *Hierarchical Listing:* The server must collapse subdirectories into single entries (like `ls`), even if the storage is a flat map.
  - *Case Insensitivity:* Paths must be stored/looked up case-insensitively, but display the original filename casing.
  - *Strict Whitelisting:* Security relies on rejecting all filenames containing non-standard characters (regex validation).

- **Deduplication:** The system must check content hashes (or byte equality) to prevent incrementing revision numbers for identical uploads.

## 3. Protocol Specification

### General

- **Transport:** TCP (Text commands + Binary payloads)
- **Delimiter:** Newline (`\n`) for commands/responses. No delimiter after binary data.
- **Prompt:** Server sends `READY\n` when awaiting input.

### Constraints

- **Filenames:** Absolute paths only (start with `/`)
- **Charset:** Alphanumeric, dot, underscore, hyphen, slash (`^[a-zA-Z0-9/._-]+$`)
- **Content:** Valid UTF-8 text files only (reject binary/control chars)

### Commands

#### `PUT <filename> <length>`

- Client sends command line → waits for newline → sends exactly `<length>` bytes.
- Server validates content is text.
- Server checks if content matches latest revision:
  - If match: Returns current revision (deduplication).
  - If new: Appends revision and increments ID.
- **Response:** `OK r<id>\n` followed by `READY\n`

#### `GET <filename> [revision]`

Retrieves file content.

**Arguments:**
- `<filename>`: Case-insensitive lookup
- `[revision]`: Optional. `rX` (specific) or omitted (latest)

**Response:** `OK <length>\n`, followed by raw bytes, followed by `READY\n`

#### `LIST <dir>`

Lists contents of a directory (non-recursive/hierarchical).

**Behavior:** Collapses all files in sub-folders into a single "DIR" entry. Output is sorted alphabetically.

**Response:**
- `OK <count>\n`
- List entries (one per line): `<name> <metadata>`
  - Files: `name.txt r3`
  - Dirs: `subdir/ DIR`
- Footer: `READY\n`

#### `HELP`

**Response:** `OK usage: HELP|GET|PUT|LIST\n` followed by `READY\n`

### Error Handling

All errors return a single line prefixed with `ERR`, followed by `READY\n`.

**Common Errors:**
- `ERR illegal file name`
- `ERR text files only`
- `ERR no such file`