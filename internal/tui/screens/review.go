package screens

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

type fileSummary struct {
	Path  string
	Size  int64
	IsDir bool
}

// ReviewCategory represents a category of files to review, with its total size, file count, and lists of files.
type ReviewCategory struct {
	Name      string
	Size      int64
	Files     int
	SizeKnown bool
	TopFiles  []fileSummary // to show the top 10 largest files in this category
	AllFiles  []fileSummary // to show all files in the review screen
}

// ReviewModel is the model for the review screen, containing all categories and their files, as well as UI state for scrolling and toggling views.
type ReviewModel struct {
	Categories     []ReviewCategory
	TotalSize      int64
	TotalFiles     int
	ExecuteMode    bool
	ShowAll        bool
	ScrollPos      int
	Cursor         int
	VisibleCount   []int // indices of categories that are currently visible based on ShowAll and size > 0
	Width          int
	Height         int
	ShowFull       bool
	UnknownCount   int
	PendingConfirm bool // true when execute mode is waiting for a second enter to confirm deletion
}

// NewReview constructs a ReviewModel from the scan results
func NewReview(results map[cleaner.Category]*cleaner.ScanResult, executeMode bool) ReviewModel {
	m := ReviewModel{ExecuteMode: executeMode}

	for _, result := range results {
		if result.TotalFiles == 0 {
			continue
		}

		sizeKnown := true
		if result.Category == cleaner.CategoryTimeMachineSnapshots {
			sizeKnown = result.SizeKnown || result.TotalFiles == 0
		}

		cat := ReviewCategory{
			Name:      string(result.Category),
			Size:      result.TotalSize,
			Files:     result.TotalFiles,
			SizeKnown: sizeKnown,
		}

		// Build file summaries.
		allFiles := make([]fileSummary, 0, len(result.Entries))
		for _, entry := range result.Entries {
			path := entry.Path
			//
			// to-do: implement friendly name for docker
			//
			allFiles = append(allFiles, fileSummary{
				Path:  path,
				Size:  entry.Size,
				IsDir: entry.IsDir,
			})
		}

		sort.Slice(allFiles, func(i, j int) bool {
			return allFiles[i].Size > allFiles[j].Size
		})

		cat.AllFiles = allFiles
		cat.TopFiles = allFiles
		// if there are more than 10 files, only show the top 10 largest in the main review screen
		if len(allFiles) > 10 {
			cat.TopFiles = allFiles[:10]
		}

		m.Categories = append(m.Categories, cat)
		m.TotalSize += result.TotalSize
		m.TotalFiles += result.TotalFiles
		if !cat.SizeKnown {
			m.UnknownCount++
		}
	}

	// sort categories by size desc
	sort.Slice(m.Categories, func(i, j int) bool {
		return m.Categories[i].Size > m.Categories[j].Size
	})

	// initialize per category visible count (top 10 by default)
	m.VisibleCount = make([]int, len(m.Categories))
	for i, cat := range m.Categories {
		limit := 10
		if len(cat.AllFiles) < 10 {
			limit = len(cat.AllFiles)
		}
		m.VisibleCount[i] = limit
	}

	m.Cursor = 0
	m.ScrollPos = 0

	return m
}

func (m *ReviewModel) ScrollUp() {
	ci, fi := m.cursorCatFile()
	if fi == 0 {
		// Already at the first file of this category — don't cross into another.
		return
	}
	m.Cursor--
	m.scrollIntoView(ci, fi-1)
}

func (m *ReviewModel) ScrollDown() {
	ci, fi := m.cursorCatFile()
	shown := 0
	if ci < len(m.VisibleCount) {
		shown = m.VisibleCount[ci]
	}
	if fi >= shown-1 {
		// Already at the last visible file of this category — don't cross into another.
		return
	}
	m.Cursor++
	m.scrollIntoView(ci, fi+1)
}

// scrollIntoView adjusts ScrollPos so the file at (ci, fi) is visible,
// scrolling the minimum amount necessary.
func (m *ReviewModel) scrollIntoView(ci, fi int) {
	viewHeight := m.Height - 10
	if viewHeight < 5 {
		viewHeight = 20
	}
	headerLine := m.headerLineIndexForCategory(ci)
	focusedLine := m.fileLineIndex(ci, fi)
	visibleEnd := m.ScrollPos + viewHeight - 1

	if focusedLine >= m.ScrollPos && focusedLine <= visibleEnd {
		// Already visible — don't scroll.
	} else if focusedLine > visibleEnd {
		// Below viewport: scroll down minimally.
		m.ScrollPos = focusedLine - viewHeight + 2
		if m.ScrollPos < 0 {
			m.ScrollPos = 0
		}
	} else {
		// Above viewport: scroll up to show the category header.
		m.ScrollPos = headerLine
	}
}

// ToggleShowAll toggles between showing top 10 and all files per category.
func (m *ReviewModel) ToggleShowAll() {
	ci, fi := m.cursorCatFile()
	m.ShowAll = !m.ShowAll
	// Adjust visible counts accordingly
	for i := range m.Categories {
		if m.ShowAll {
			m.VisibleCount[i] = len(m.Categories[i].AllFiles)
		} else {
			limit := 10
			if len(m.Categories[i].AllFiles) < limit {
				limit = len(m.Categories[i].AllFiles)
			}
			m.VisibleCount[i] = limit
		}
	}
	// When collapsing, clamp cursor if it's now beyond the visible range.
	if !m.ShowAll {
		shown := 0
		if ci < len(m.VisibleCount) {
			shown = m.VisibleCount[ci]
		}
		if shown > 0 && fi >= shown {
			fi = shown - 1
			m.Cursor = m.globalFileIndexFor(ci, fi)
		}
	}
	m.scrollIntoView(ci, fi)
}

// ToggleFullPath toggles between shortened and full path display.
func (m *ReviewModel) ToggleFullPath() {
	m.ShowFull = !m.ShowFull
}

// SetSize updates dimensions.
func (m *ReviewModel) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

// totalFiles returns the total number of files across all categories.
func (m ReviewModel) totalFiles() int {
	n := 0
	for _, c := range m.Categories {
		n += len(c.AllFiles)
	}
	return n
}

// globalFileIndexFor returns the global file index across all categories for (ci, fi).
func (m ReviewModel) globalFileIndexFor(ci, fi int) int {
	if ci < 0 || ci >= len(m.Categories) {
		return 0
	}
	if fi < 0 {
		fi = 0
	}
	idx := 0
	for c := 0; c < ci; c++ {
		idx += len(m.Categories[c].AllFiles)
	}
	if fi > len(m.Categories[ci].AllFiles)-1 {
		fi = len(m.Categories[ci].AllFiles) - 1
		if fi < 0 {
			fi = 0
		}
	}
	return idx + fi
}

// NextCategory moves focus to the next category and adjusts scroll.
func (m *ReviewModel) NextCategory() {
	if len(m.Categories) <= 1 {
		return
	}
	ci, _ := m.cursorCatFile()
	// Find next category with files (wrap around)
	start := (ci + 1) % len(m.Categories)
	next := start
	for tries := 0; tries < len(m.Categories); tries++ {
		if len(m.Categories[next].AllFiles) > 0 {
			break
		}
		next = (next + 1) % len(m.Categories)
	}
	if len(m.Categories[next].AllFiles) == 0 {
		return
	}
	// Always start at the first file of the new category.
	fi := 0
	// Move cursor to the corresponding global index
	m.Cursor = m.globalFileIndexFor(next, fi)
	m.scrollIntoView(next, fi)
}

// headerLineIndexForCategory computes the line index of the header for category i.
func (m ReviewModel) headerLineIndexForCategory(i int) int {
	line := 0
	for c := 0; c < i; c++ {
		line++ // header
		if !m.Categories[c].SizeKnown {
			line++ // warning line shown before files for unknown-size categories
		}
		shown := 0
		if c < len(m.VisibleCount) {
			shown = m.VisibleCount[c]
		}
		if shown > len(m.Categories[c].AllFiles) {
			shown = len(m.Categories[c].AllFiles)
		}
		line += shown
		// more line if hidden remain and not ShowAll
		remaining := len(m.Categories[c].AllFiles) - shown
		if !m.ShowAll && remaining > 0 {
			line++
		}
		line++ // spacer
	}
	return line
}

// fileLineIndex returns the rendered line index of the file at position fi within category ci.
func (m ReviewModel) fileLineIndex(ci, fi int) int {
	line := m.headerLineIndexForCategory(ci)
	line++ // the header line itself
	if !m.Categories[ci].SizeKnown {
		line++ // warning line before files
	}
	return line + fi
}

// cursorCatFile returns (categoryIndex, fileIndexWithinCategory) for the current cursor.
func (m ReviewModel) cursorCatFile() (int, int) {
	idx := m.Cursor
	for i, c := range m.Categories {
		if idx < len(c.AllFiles) {
			return i, idx
		}
		idx -= len(c.AllFiles)
	}
	if len(m.Categories) == 0 {
		return 0, 0
	}
	last := len(m.Categories) - 1
	return last, len(m.Categories[last].AllFiles) - 1
}

func (m ReviewModel) View() string {
	var b strings.Builder

	modeTag := styles.Warning.Bold(true).Render("[DRY RUN]")
	if m.ExecuteMode {
		modeTag = styles.Error.Bold(true).Render("[EXECUTE]")
	}
	title := fmt.Sprintf("Review: %s across %d files", utils.FormatBytes(m.TotalSize), m.TotalFiles)
	if m.UnknownCount > 0 {
		title = fmt.Sprintf("%s + %d unknown-size categor%s", title, m.UnknownCount, pluralSuffix(m.UnknownCount, "y", "ies"))
	}
	b.WriteString(modeTag + " " + styles.Title.Render(title))

	b.WriteString("\n\n")

	if m.TotalFiles == 0 {
		b.WriteString("No files to clean! All categories are already tidy! 🎉")
		b.WriteString("\n")
		b.WriteString(styles.Help.Render("  Press q to quit"))
		return b.String()
	}

	lines := []string{}
	type section struct {
		headerStr string
		headerIdx int
	}

	var sections []section

	globalFileIdx := 0
	for ci, cat := range m.Categories {
		sizeLabel := utils.FormatBytes(cat.Size)
		if !cat.SizeKnown {
			sizeLabel = "unknown size"
		}
		hdr := styles.CategoryHeader.Render(fmt.Sprintf("  %s (%s, %d files)", cat.Name, sizeLabel, cat.Files))

		sections = append(sections, section{
			headerStr: hdr,
			headerIdx: len(lines),
		})
		lines = append(lines, hdr)

		if !cat.SizeKnown {
			lines = append(lines, styles.Warning.Render("    Note: APFS snapshots don't expose a reliable reclaimable size — excluded from the total."))
		}

		shown := 0
		if ci < len(m.VisibleCount) {
			shown = m.VisibleCount[ci]
		}

		if shown > len(cat.AllFiles) {
			shown = len(cat.AllFiles)
		}

		for fi := 0; fi < shown; fi++ {
			f := cat.AllFiles[fi]
			short := displayPath(cat.Name, f, m.ShowFull)
			sizeText := utils.FormatBytes(f.Size)
			if !cat.SizeKnown {
				sizeText = "unknown"
			}
			line := fmt.Sprintf("    %s (%s)", styles.Dim.Render(short), sizeText)
			if globalFileIdx == m.Cursor {
				line = fmt.Sprintf("  > %s (%s)", styles.Highlight.Render(short), sizeText)
			}
			lines = append(lines, line)
			globalFileIdx++
		}
		// Advance past hidden files so globalFileIdx stays in sync with m.Cursor,
		// which is always based on AllFiles counts (not VisibleCount).
		globalFileIdx += len(cat.AllFiles) - shown

		remaining := len(cat.AllFiles) - shown
		if !m.ShowAll && remaining > 0 {
			lines = append(lines, styles.More.Render(fmt.Sprintf("    + %d more files [a: to show all]", remaining)))
		}

		lines = append(lines, "") // spacer
	}

	viewHeight := m.Height - 10 // scrolling with sticky category header
	if viewHeight < 5 {
		viewHeight = 20
	}

	start := m.ScrollPos
	if start < 0 {
		start = 0
	}

	if start > len(lines) {
		start = len(lines)
	}

	pinnedHeader := ""
	currentHeaderIdx := 0
	if len(sections) > 0 {
		secIdx := 0
		for i := range sections {
			if sections[i].headerIdx <= start {
				secIdx = i
			} else {
				break
			}
		}
		pinnedHeader = sections[secIdx].headerStr
		currentHeaderIdx = sections[secIdx].headerIdx
	}

	if pinnedHeader != "" {
		b.WriteString(pinnedHeader + "\n")
	}

	// content slice under the pinned header
	displayStart := start
	if displayStart == currentHeaderIdx {
		displayStart++ // skip the header since it's pinned
	}

	if displayStart > len(lines) {
		displayStart = len(lines)
	}

	// this is to ensure we don't slice beyond the available lines
	remain := viewHeight - 1
	if remain < 1 {
		remain = 1
	}

	end := displayStart + remain
	if end > len(lines) {
		end = len(lines)
	}

	for _, line := range lines[displayStart:end] {
		b.WriteString(line + "\n")
	}

	showAllHintTxt := "a: show all files"
	if m.ShowAll {
		showAllHintTxt = "a: collapse to top 10"
	}

	fullHintTxt := "f: show full paths"
	if m.ShowFull {
		fullHintTxt = "f: show short paths"
	}

	var switchListHintTxt string
	if len(m.Categories) > 1 {
		switchListHintTxt = "tab: switch category"
	}

	// Position indicator: show current category and visible/total file count.
	curCi, _ := m.cursorCatFile()
	if curCi >= 0 && curCi < len(m.Categories) {
		curCat := m.Categories[curCi]
		curShown := 0
		if curCi < len(m.VisibleCount) {
			curShown = m.VisibleCount[curCi]
		}
		if curShown > len(curCat.AllFiles) {
			curShown = len(curCat.AllFiles)
		}
		_, curFi := m.cursorCatFile()
		catPos := fmt.Sprintf("  %s  [file %d/%d]", curCat.Name, curFi+1, curShown)
		if len(curCat.AllFiles) > curShown {
			catPos = fmt.Sprintf("  %s  [file %d/%d, %d more not shown]", curCat.Name, curFi+1, curShown, len(curCat.AllFiles)-curShown)
		}
		if len(m.Categories) > 1 {
			catPos += fmt.Sprintf("  (%d/%d categories)", curCi+1, len(m.Categories))
		}
		b.WriteString(styles.Muted.Render(catPos) + "\n")
	}

	if m.PendingConfirm {
		b.WriteString(styles.Error.Bold(true).Render(fmt.Sprintf(
			"  !! Permanently delete %s across %d files? Press enter to confirm or esc to cancel.",
			utils.FormatBytes(m.TotalSize), m.TotalFiles,
		)))
	} else if m.ExecuteMode {
		if switchListHintTxt != "" {
			b.WriteString(styles.Help.Render(fmt.Sprintf("  enter: DELETE files |  %s  |  %s  |  %s  | esc: back to dashboard | j/k: scroll", showAllHintTxt, fullHintTxt, switchListHintTxt)))
		} else {
			b.WriteString(styles.Help.Render(fmt.Sprintf("  enter: DELETE files |  %s  |  %s  | esc: back to dashboard | j/k: scroll", showAllHintTxt, fullHintTxt)))
		}
	} else {
		if switchListHintTxt != "" {
			b.WriteString(styles.Help.Render(fmt.Sprintf("  enter: SIMULATE (dry run) |  %s  |  %s  |  %s  | esc: back to dashboard | j/k: scroll", showAllHintTxt, fullHintTxt, switchListHintTxt)))
		} else {
			b.WriteString(styles.Help.Render(fmt.Sprintf("  enter: SIMULATE (dry run) |  %s  |  %s  | esc: back to dashboard | j/k: scroll", showAllHintTxt, fullHintTxt)))
		}
	}

	return b.String()
}

// displayPath formats a path for display, with special handling for caches.
func displayPath(category string, f fileSummary, showFull bool) string {
	path := f.Path
	// Home substitution if possible
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home) {
		path = "~" + path[len(home):]
	}

	// If full path display is enabled, do not elide or transform (except ~)
	if showFull {
		return path
	}
	// Friendly display for docker grouping paths like docker://type/name
	if strings.HasPrefix(path, "docker://") {
		rest := strings.TrimPrefix(path, "docker://")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("docker %s: %s", parts[0], parts[1])
		}
		return "docker " + rest
	}

	if category == string(cleaner.CategoryTimeMachineSnapshots) {
		if date, ok := snapshotDateFromDisplayPath(path); ok {
			return "snapshot " + date
		}
	}

	// Special formatting for caches: ~/Library/Caches/<APP>/<...>/<name>
	if category == string(cleaner.CategoryCaches) {
		p := path
		// Ensure we have a consistent base prefix with ~ if under home
		if strings.HasPrefix(p, "~/") {
			// ok
		}

		// Split on '/'
		parts := strings.Split(p, "/")
		// Expect: ["~","Library","Caches", app, ...]
		if len(parts) >= 4 && parts[0] == "~" && parts[1] == "Library" && parts[2] == "Caches" {
			app := parts[3]
			// Determine last element name (file or dir)
			name := ""
			if len(parts) > 4 {
				name = parts[len(parts)-1]
			} else {
				// Path ends at app level
				name = app
			}
			// If there are more than one segment between app and last name, elide middle
			if len(parts) > 5 {
				return fmt.Sprintf("~/Library/Caches/%s/<...>/%s", app, name)
			}
			// If exactly one extra segment beyond app, show it directly
			if len(parts) == 5 {
				return fmt.Sprintf("~/Library/Caches/%s/%s", app, name)
			}
			// Only up to the app folder
			return fmt.Sprintf("~/Library/Caches/%s", app)
		}
		// If not matching the expected pattern, fall back to general shortening below
	}

	// General shortening: keep tail for very long paths
	if len(path) > 50 {
		return "..." + path[len(path)-47:]
	}
	return path
}

func pluralSuffix(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func snapshotDateFromDisplayPath(path string) (string, bool) {
	const (
		prefix = "com.apple.TimeMachine."
		suffix = ".local"
	)

	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}

	date := strings.TrimPrefix(path, prefix)
	date = strings.TrimSuffix(date, suffix)
	if date == "" {
		return "", false
	}

	return date, true
}
