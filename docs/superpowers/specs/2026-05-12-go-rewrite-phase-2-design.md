# Design Spec: Disk Explorer Go Rewrite - Phase 2 (TUI & Remote)

**Date:** 2026-05-12
**Status:** Approved
**Topic:** Expanding the Go rewrite with full TUI navigation, real-time scanner integration, and native SSH remote execution.

## 1. Overview
Phase 2 transitions the Go rewrite from a foundation to a functional tool. It focuses on creating a usable interactive interface that displays scan results in real-time and enables remote scanning via a robust, native Go SSH implementation.

## 2. Core Components

### 2.1 TUI Expansion (`internal/tui`)
*   **Navigation & Browsing (Priority):**
    *   Implement a list view for directory entries.
    *   Support `Up`/`Down` (or `k`/`j`) for navigation.
    *   `Enter` to descend into a directory, `Backspace` or `left` to go up.
    *   `q` or `Esc` to exit.
*   **Real-time Updates:**
    *   The TUI will listen to the scanner's channel and update the view as results arrive.
    *   Use `tea.Cmd` to send scan results back into the Bubble Tea loop.

### 2.2 Scanner Integration (`internal/scanner`)
*   Refactor the existing parallel scanner to be more easily controllable from the TUI.
*   Ensure the scanner can be interrupted if the user changes directory or exits.

### 2.3 Remote Execution (`internal/remote`)
*   **Implementation:** Use `golang.org/x/crypto/ssh` for native SSH support.
*   **Features:**
    *   Key-based authentication support (standard paths like `~/.ssh/id_rsa`).
    *   Agent support via `SSH_AUTH_SOCK`.
    *   Execution of the embedded `disk-explorer.sh` script on the remote host.
    *   Parsing of the remote output back into `scanner.Entry` objects.

## 3. Data Flow
1.  **User starts scan** (local path or remote host).
2.  **Scanner starts** in a background goroutine, sending `Entry` objects over a channel.
3.  **TUI Model** receives entries via Bubble Tea messages.
4.  **View re-renders** with updated sizes and file lists.
5.  **User navigates**, triggering new scans (if not already cached) and UI updates.

## 4. Success Criteria
*   The user can navigate through a local or remote file system using the TUI.
*   The UI updates dynamically as the scan progresses.
*   SSH connections are established and scripts executed without relying on the system's `ssh` binary.

## 5. Next Steps
1.  Write the implementation plan using the `writing-plans` skill.
2.  Implement the TUI navigation logic.
3.  Implement the native Go SSH client.
4.  Connect the scanner to the TUI.
