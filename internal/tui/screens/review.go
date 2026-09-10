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

// ConfirmState represents the pending confirmation step in the review flow.
type ConfirmState int

const (
	ConfirmNone        ConfirmState = iota
	ConfirmSudo                     // waiting for user to confirm sudo-warning before proceeding
	ConfirmExecute                  // waiting for user to confirm execute-mode deletion
	ConfirmRevalidated              // waiting for user to re-confirm after revalidation found a material change
)

// RevalidationDelta summarizes what changed when the approved plan was
// re-checked against disk and the current config immediately before
// cleaning -- see internal/tui.revalidatePlan. A nil *RevalidationDelta on
// ReviewModel means revalidation hasn't run yet (or found nothing material);
// this type only exists to describe a material change worth showing the user.
type RevalidationDelta struct {
	MissingFiles     int
	TypeChangedFiles int
	NewlyProtected   int
	IdentityChanged  int
	SizeChanged      bool
	TotalSize        int64
	TotalFiles       int
}

// Material reports whether this delta is worth interrupting the user for --
// anything that shrinks or alters what was approved, per the same "never
// silently clean less than reviewed, never more" rule the rest of the
// revalidation pipeline follows. IdentityChanged counts an entry whose
// on-disk identity (Dev/Ino) no longer matches what it was at scan time --
// dropped from the plan the same way a missing or type-changed entry is.
// Material deliberately does NOT include SizeChanged on its own. Missing,
// type-changed, and newly-protected entries all change what will actually
// be deleted or skipped -- a size drift alone (a log or cache file that grew
// a few bytes between scan and confirm, the common case for Logs/Caches/Temp
// on a live system) does not, and gating a re-confirmation on it would fire
// on nearly every run, training the user to press enter twice reflexively --
// exactly what would blunt this screen for the changes that do matter.
// SizeChanged is still computed, but -- deliberately, per the above -- has
// no rendering of its own in View(): TotalSize/TotalFiles (which already
// reflect any size drift) are shown whenever a summary IS displayed for one
// of the other reasons, but a size-only drift with nothing else material
// produces no summary at all and is accepted silently by design.
func (d RevalidationDelta) Material() bool {
	return d.MissingFiles > 0 || d.TypeChangedFiles > 0 || d.NewlyProtected > 0 || d.IdentityChanged > 0
}

type fileSummary struct {
	Path      string
	Size      int64
	IsDir     bool
	Protected bool
}

// ReviewCategory represents a category of files to review, with its total size, file count, and lists of files.
type ReviewCategory struct {
	Name      string
	Category  cleaner.Category
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
	IsElevated     bool
	ShowAll        bool
	ScrollPos      int
	Cursor         int
	VisibleCount   []int // indices of categories that are currently visible based on ShowAll and size > 0
	Width          int
	Height         int
	ShowFull       bool
	UnknownCount   int
	SudoCategories []cleaner.Category
	ConfirmState   ConfirmState

	// RevalidationDelta is set right before entering ConfirmRevalidated, and
	// rendered by the ConfirmRevalidated case in View(). nil otherwise.
	RevalidationDelta *RevalidationDelta

	// RevalidationErr holds a structural failure from revalidatePlan itself
	// (as opposed to a per-category failure, which is data, not an error) --
	// e.g. an unexpected registry mismatch. Rendered as a banner; ConfirmState
	// stays ConfirmNone so pressing enter again simply retries.
	RevalidationErr error

	// Revalidating is true while a revalidateCmd dispatched from this
	// confirm is in flight -- it can take a while (Docker/Time Machine
	// shell-outs, tens of thousands of Lstats for a large category), and
	// without some visible feedback here an enter press that seems to do
	// nothing invites pressing it again, which App.revalidating (not this
	// field) is what actually guards against.
	Revalidating bool

	// AuthenticateSudo is the pending choice on the ConfirmSudo dialog: true
	// authenticates via sudo for the categories in SudoCategories, false
	// skips them. Defaults to false (skip) so that the same "press enter a
	// few times" muscle memory used before this dialog existed keeps its old
	// meaning -- an unprivileged clean -- rather than silently starting to
	// authenticate as root. It also means a warm sudo timestamp cache (sudo
	// caches successful auth for ~5 minutes on macOS) can never authenticate
	// the user without an explicit, deliberate switch to the other option.
	AuthenticateSudo bool
}

// NewReview constructs a ReviewModel from the scan results
func NewReview(results map[cleaner.Category]*cleaner.ScanResult, executeMode bool, registry *cleaner.Registry, isElevated bool) ReviewModel {
	m := ReviewModel{
		ExecuteMode: executeMode,
		IsElevated:  isElevated,
	}

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
			Category:  result.Category,
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
				Path:      path,
				Size:      entry.Size,
				IsDir:     entry.IsDir,
				Protected: entry.Protected,
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
		if registry != nil {
			if c, ok := registry.Get(result.Category); ok && c.RequiresSudo() {
				m.SudoCategories = append(m.SudoCategories, result.Category)
			}
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

func (m ReviewModel) ShouldWarnAboutSudo() bool {
	return m.ExecuteMode && !m.IsElevated && len(m.SudoCategories) > 0
}

func (m ReviewModel) actionableTotals() (int64, int) {
	blocked := make(map[cleaner.Category]struct{}, len(m.SudoCategories))
	// AuthenticateSudo means the user chose to authenticate for these
	// categories, so they are still actionable and count toward the total.
	// Only exclude them here when the user chose to skip instead.
	if m.ShouldWarnAboutSudo() && !m.AuthenticateSudo {
		for _, cat := range m.SudoCategories {
			blocked[cat] = struct{}{}
		}
	}

	var totalSize int64
	var totalFiles int
	for _, cat := range m.Categories {
		if _, isBlocked := blocked[cat.Category]; isBlocked {
			continue
		}
		for _, f := range cat.AllFiles {
			if f.Protected {
				continue
			}
			totalSize += f.Size
			totalFiles++
		}
	}

	return totalSize, totalFiles
}

// sudoTotals returns the size and file count across only the categories in
// SudoCategories, mirroring actionableTotals' protected-path exclusion. Used
// to describe what a sudo authentication decision is actually about.
func (m ReviewModel) sudoTotals() (int64, int) {
	sudo := make(map[cleaner.Category]struct{}, len(m.SudoCategories))
	for _, cat := range m.SudoCategories {
		sudo[cat] = struct{}{}
	}

	var totalSize int64
	var totalFiles int
	for _, cat := range m.Categories {
		if _, isSudo := sudo[cat.Category]; !isSudo {
			continue
		}
		for _, f := range cat.AllFiles {
			if f.Protected {
				continue
			}
			totalSize += f.Size
			totalFiles++
		}
	}

	return totalSize, totalFiles
}

func (c ReviewCategory) CategoryDisplayName() string {
	return cleaner.Category(c.Name).DisplayName()
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

	switch {
	case focusedLine >= m.ScrollPos && focusedLine <= visibleEnd:
		// Already visible — don't scroll.
	case focusedLine > visibleEnd:
		// Below viewport: scroll down minimally.
		m.ScrollPos = focusedLine - viewHeight + 2
		if m.ScrollPos < 0 {
			m.ScrollPos = 0
		}
	default:
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

	if m.ShouldWarnAboutSudo() {
		sudoNames := make([]string, len(m.SudoCategories))
		for i, cat := range m.SudoCategories {
			sudoNames[i] = cat.DisplayName()
		}
		warning := fmt.Sprintf(
			"Some selected categories require administrator access: %s.",
			strings.Join(sudoNames, ", "),
		)
		b.WriteString(styles.Warning.Render("  Warning: " + warning))
		b.WriteString("\n")
		b.WriteString(styles.Help.Render("  Press enter to choose whether to authenticate with sudo or skip them."))
		b.WriteString("\n\n")
	}

	if m.TotalFiles == 0 {
		if m.RevalidationDelta != nil {
			// This plan was not empty a moment ago -- it was approved,
			// reviewed, and confirmed, and revalidation is what emptied it
			// (every entry vanished, changed type, or became protected
			// between review and confirm). Reusing the plain "already
			// tidy" wording below would read as if nothing had ever been
			// found, hiding exactly the information the user most needs
			// here.
			d := m.RevalidationDelta
			b.WriteString("The approved plan is now empty:")
			b.WriteString("\n")
			if d.MissingFiles > 0 {
				fmt.Fprintf(&b, "  - %d item(s) no longer exist\n", d.MissingFiles)
			}
			if d.TypeChangedFiles > 0 {
				fmt.Fprintf(&b, "  - %d item(s) changed type\n", d.TypeChangedFiles)
			}
			if d.NewlyProtected > 0 {
				fmt.Fprintf(&b, "  - %d item(s) are now protected\n", d.NewlyProtected)
			}
			if d.IdentityChanged > 0 {
				fmt.Fprintf(&b, "  - %d item(s) changed on disk\n", d.IdentityChanged)
			}
			b.WriteString("\n")
			b.WriteString(styles.Help.Render("  esc: back to dashboard  |  q: quit"))
			return b.String()
		}
		b.WriteString("No files to clean! All categories are already tidy! 🎉")
		b.WriteString("\n")
		b.WriteString(styles.Help.Render("  Press q to quit"))
		return b.String()
	}

	if m.RevalidationErr != nil {
		b.WriteString(styles.Error.Render(fmt.Sprintf("  Failed to revalidate the plan: %v", m.RevalidationErr)))
		b.WriteString("\n")
		b.WriteString(styles.Help.Render("  Press enter to retry."))
		b.WriteString("\n\n")
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
			lockedTag := ""
			if f.Protected {
				lockedTag = styles.SafetyBadgeDoNotTouch.Render("LOCKED") + " "
			}
			line := fmt.Sprintf("    %s%s (%s)", lockedTag, styles.Dim.Render(short), sizeText)
			if globalFileIdx == m.Cursor {
				line = fmt.Sprintf("  > %s%s (%s)", lockedTag, styles.Highlight.Render(short), sizeText)
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

	switch {
	case m.Revalidating:
		b.WriteString(styles.Help.Render("  Revalidating the plan against disk... please wait"))

	case m.ConfirmState == ConfirmSudo:
		sudoNames := make([]string, len(m.SudoCategories))
		for i, cat := range m.SudoCategories {
			sudoNames[i] = cat.DisplayName()
		}
		sudoSize, sudoFiles := m.sudoTotals()
		b.WriteString(styles.Warning.Bold(true).Render(fmt.Sprintf(
			"  Administrator access needed for %s (%s across %d files): %s",
			pluralSuffix(len(sudoNames), "this category", "these categories"),
			utils.FormatBytes(sudoSize), sudoFiles, strings.Join(sudoNames, ", "),
		)))
		b.WriteString("\n\n")

		authLabel, skipLabel := "Authenticate with sudo", "Skip these categories"
		if m.AuthenticateSudo {
			b.WriteString("  " + styles.Highlight.Render("> "+authLabel) + "\n")
			b.WriteString("  " + styles.Dim.Render("  "+skipLabel) + "\n")
		} else {
			b.WriteString("  " + styles.Dim.Render("  "+authLabel) + "\n")
			b.WriteString("  " + styles.Highlight.Render("> "+skipLabel) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(styles.Help.Render("  enter: confirm choice  |  up/down: switch  |  esc: back to review"))

	case m.ConfirmState == ConfirmExecute:
		totalSize, totalFiles := m.actionableTotals()
		message := fmt.Sprintf(
			"  !! Permanently delete %s across %d files? Press enter to confirm or esc to cancel.",
			utils.FormatBytes(totalSize), totalFiles,
		)
		switch {
		case m.ShouldWarnAboutSudo() && !m.AuthenticateSudo:
			message = fmt.Sprintf(
				"  !! Permanently delete %s across %d files and skip %d sudo-protected categor%s? Press enter to confirm or esc to cancel.",
				utils.FormatBytes(totalSize),
				totalFiles,
				len(m.SudoCategories),
				pluralSuffix(len(m.SudoCategories), "y", "ies"),
			)
		case m.ShouldWarnAboutSudo() && m.AuthenticateSudo:
			message = fmt.Sprintf(
				"  !! Permanently delete %s across %d files, including %d sudo-authenticated categor%s (sudo may prompt for your password)? Press enter to confirm or esc to cancel.",
				utils.FormatBytes(totalSize),
				totalFiles,
				len(m.SudoCategories),
				pluralSuffix(len(m.SudoCategories), "y", "ies"),
			)
		}
		b.WriteString(styles.Error.Bold(true).Render(message))

	case m.ConfirmState == ConfirmRevalidated && m.RevalidationDelta != nil:
		d := m.RevalidationDelta
		b.WriteString(styles.Warning.Bold(true).Render("  The plan changed since it was reviewed:"))
		b.WriteString("\n")
		if d.MissingFiles > 0 {
			fmt.Fprintf(&b, "    - %d item(s) no longer exist -- will be skipped\n", d.MissingFiles)
		}
		if d.TypeChangedFiles > 0 {
			fmt.Fprintf(&b, "    - %d item(s) changed type -- will be skipped\n", d.TypeChangedFiles)
		}
		if d.NewlyProtected > 0 {
			fmt.Fprintf(&b, "    - %d item(s) are now protected -- will be skipped\n", d.NewlyProtected)
		}
		if d.IdentityChanged > 0 {
			fmt.Fprintf(&b, "    - %d item(s) changed on disk -- will be skipped\n", d.IdentityChanged)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "  New plan: %s · %d files\n\n", utils.FormatBytes(d.TotalSize), d.TotalFiles)
		if m.ExecuteMode {
			b.WriteString(styles.Help.Render("  enter: confirm updated plan  |  esc: back to review"))
		} else {
			b.WriteString(styles.Help.Render("  enter: continue (dry run, nothing will be deleted)  |  esc: back to review"))
		}

	case m.ExecuteMode:
		if switchListHintTxt != "" {
			b.WriteString(styles.Help.Render(fmt.Sprintf("  enter: DELETE files |  %s  |  %s  |  %s  | esc: back to dashboard | j/k: scroll", showAllHintTxt, fullHintTxt, switchListHintTxt)))
		} else {
			b.WriteString(styles.Help.Render(fmt.Sprintf("  enter: DELETE files |  %s  |  %s  | esc: back to dashboard | j/k: scroll", showAllHintTxt, fullHintTxt)))
		}
	default:
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
	// Display only: a file name or Docker tag carrying control characters
	// must not be able to inject rows or escape sequences into the screen.
	path := utils.SanitizeForTerminal(f.Path)
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
	if category == string(cleaner.CategoryApplicationCaches) {
		p := path

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
