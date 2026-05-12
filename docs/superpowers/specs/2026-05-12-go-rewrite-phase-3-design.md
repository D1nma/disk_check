# Design Spec: Disk Explorer Go Rewrite - Phase 3 (TUI Deepening)

**Date:** 2026-05-12
**Status:** Approved
**Topic:** Implementing advanced TUI navigation (Lazy Scanning) and dynamic sorting.

## 1. Overview
Phase 3 focuses on making the TUI a truly usable explorer. Instead of a single flat list, we implement a hierarchical "Lazy Scan" navigation strategy and add the ability to sort entries dynamically.

## 2. Core Components

### 2.1 Navigation & Browsing (`internal/tui`)
*   **Lazy Scanning Strategy (B):**
    *   When the user enters a directory (`Enter`), the current scan is cancelled.
    *   A new scan is initiated for the selected directory only.
    *   The model keeps a "stack" of parent directories to allow navigating back up (`Backspace` / `Left`).
    *   This ensures minimal memory usage as only the current view's entries are held in the model.
*   **Breadcrumbs:** Update the header to show the full current path.

### 2.2 Dynamic Sorting (`internal/scanner` & `internal/tui`)
*   **Default Sort (A):** Size (Largest first).
*   **Sorting Keys:**
    *   `s`: Sort by Size (toggle Descending/Ascending).
    *   `n`: Sort by Name (toggle Ascending/Descending).
    *   `t`: Sort by Time/Date (toggle Newest/Oldest).
*   **Implementation:** Entries will be sorted in-place in the `Model` whenever a new entry arrives or the sort key is toggled.

### 2.3 Visual Improvements
*   Add a progress indicator or spinner while the "Lazy Scan" is running for the current folder.
*   Implement basic "Percentage Bar" in the list view (relative to the largest item in the current view).

## 3. Data Flow
1.  **User Enters Folder:** `Update()` cancels current `ScannerChan`, clears `Entries`, and starts `scanner.Scan(newPath)`.
2.  **Streaming Results:** `NewEntryMsg` arrives, added to `Entries`, and `sort.Slice` is called to maintain order.
3.  **User Navigates Up:** `Update()` pops from path stack, restarts scan at parent.

## 4. Success Criteria
*   User can navigate deep into subdirectories and back up.
*   Memory usage remains low even when browsing large trees.
*   Dynamic sorting works instantly across different keys.

## 5. Next Steps
1.  Update `internal/scanner` to support cancellation (Context).
2.  Update `internal/tui` to manage navigation stack and path transitions.
3.  Implement sorting logic in the model.
4.  Add visual polish (percentage bars).
