package toc

import (
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/grovetools/core/tui/components/markdown"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/tui/widget"
	"github.com/muesli/termenv"
)

// RenderOptions parameterizes the non-interactive outline renderers.
type RenderOptions struct {
	// Width is the render width in terminal cells. Non-positive falls back to
	// 80.
	Width int
	// Provider names the assistant role label ("claude" renders as "Claude").
	Provider string
	// ShowMarkdown renders assistant prose and user prompts as boxed transcript
	// bubbles beneath their rows, mirroring the drawer's expanded-markdown mode.
	// When false, rows keep their one-line summaries. DefaultRenderOptions sets
	// it true.
	ShowMarkdown bool
}

// DefaultRenderOptions is the renderer's canonical configuration: boxed
// markdown on, at the given width and provider.
func DefaultRenderOptions(width int, provider string) RenderOptions {
	return RenderOptions{Width: width, Provider: provider, ShowMarkdown: true}
}

// styledProfileOnce pins the process's lipgloss color profile so RenderStyled
// is deterministic: without a terminal on stdout, lipgloss auto-detects Ascii
// and silently strips every style, making "styled" output equal plain output in
// exactly the headless contexts (artifact writers, tests) that ask for styling.
var styledProfileOnce sync.Once

func ensureStyledProfile() {
	styledProfileOnce.Do(func() {
		if lipgloss.ColorProfile() == termenv.Ascii {
			lipgloss.SetColorProfile(termenv.ANSI256)
		}
	})
}

// RenderStyled renders the full outline with ANSI styling — role-colored
// transcript boxes, heading and tool icons, status icons, path-marker coloring
// — the way the interactive drawer draws it, minus cursor, selection, fold
// state (everything renders expanded) and footer. It forces a real color
// profile on first use so output stays styled off-terminal.
func RenderStyled(items []Item, opts RenderOptions) string {
	ensureStyledProfile()
	return renderOutline(items, opts)
}

// RenderPlain renders the same layout with every ANSI escape stripped. All
// width math in the styled path is ANSI-aware, so stripping preserves
// alignment exactly.
func RenderPlain(items []Item, opts RenderOptions) string {
	ensureStyledProfile()
	return ansi.Strip(renderOutline(items, opts))
}

func renderOutline(items []Item, opts RenderOptions) string {
	width := opts.Width
	if width <= 0 {
		width = 80
	}
	var b strings.Builder
	markerWidth := max(1, lipgloss.Width(theme.IconArrowRightBold))
	for i := 0; i < len(items); i++ {
		item := items[i]
		itemIndex := i
		groupEnd := i + 1
		if opts.ShowMarkdown && item.Kind == "assistant" {
			for groupEnd < len(items) && items[groupEnd].Kind == "heading" && items[groupEnd].EntryID == item.EntryID && items[groupEnd].Level > item.Level {
				groupEnd++
			}
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		// The selection-marker gutter is kept (empty) so rows align exactly as
		// they do in the interactive drawer.
		prefix := PadIconCell("", markerWidth) + " "
		prefix += theme.DefaultTheme.Muted.Render(HierarchyPrefix(item.Level))
		if item.Kind == "heading" {
			level := item.Level
			if item.HeadingLevel > 0 {
				level = item.HeadingLevel
			}
			icon, style := MarkdownHeadingIcon(level)
			prefix += style.Render(icon) + " "
		} else if icon, style := ItemIconAndStyle(item); icon != "" {
			prefix += style.Render(icon) + " "
		}
		suffix := ""
		if !opts.ShowMarkdown && item.HasKids {
			// Everything renders expanded, so the trailing chevron always points
			// down — it marks a fold root, not an interaction.
			suffix = " " + theme.DefaultTheme.Muted.Render(theme.IconChevronDown)
		}
		if item.Status != "" {
			icon, style := StatusIcon(item.Status)
			suffix += " " + style.Render(icon)
		}
		titleText := ItemTitle(item)
		if opts.ShowMarkdown && item.Kind == "prompt" {
			titleText = "You"
		} else if opts.ShowMarkdown && item.Kind == "assistant" {
			titleText = AgentDisplayName(opts.Provider)
		}
		prefix = ansi.Truncate(prefix, max(0, width-1), "")
		available := max(0, width-lipgloss.Width(prefix)-lipgloss.Width(suffix))
		title := ansi.Truncate(titleText, available, "")
		block := ansi.Truncate(prefix+title+suffix, width, "")
		if opts.ShowMarkdown && item.Kind == "assistant" && strings.TrimSpace(item.Markdown) != "" {
			box, _ := RenderAgentMarkdownBox(item.Markdown, items[itemIndex+1:groupEnd], -1, itemIndex+1, width)
			if box != "" {
				block += "\n" + box
			}
			i = groupEnd - 1
		}
		if opts.ShowMarkdown && item.Kind == "prompt" && strings.TrimSpace(item.Markdown) != "" {
			if box := RenderMarkdownTextBox(item.Markdown, width); box != "" {
				block += "\n" + box
			}
		}
		b.WriteString(block)
	}
	return b.String()
}

// ItemTitle renders a row's title text: reasoning rows are muted/italicized
// with authored bold delimiters removed, and every row gets its path markers
// colored.
func ItemTitle(item Item) string {
	title := item.Title
	if item.Kind == "reasoning" {
		// Reasoning summaries are already uniformly italicized; remove authored
		// bold delimiters instead of exposing literal ** or layering bold on top.
		title = strings.ReplaceAll(title, "**", "")
		title = HighlightPathMarkers(title)
		return theme.DefaultTheme.Muted.Italic(true).Render(title)
	}
	return HighlightPathMarkers(title)
}

// HighlightPathMarkers colors every stand-in the outline substitutes for one of
// the session's own directories ($WT/$JA/$NB), so an elided prefix cannot be
// misread as a relative path the agent actually typed. Truncation downstream is
// ANSI-aware, so a clipped row drops the marker's cells without stranding its
// escapes.
//
// The tokens come from the outline's own vocabulary rather than a local list,
// so a marker added there is colored here without a second edit.
func HighlightPathMarkers(title string) string {
	for _, token := range MarkerTokens() {
		if !strings.Contains(title, token) {
			continue
		}
		title = strings.ReplaceAll(title, token, MarkerStyle().Render(token))
	}
	return title
}

// MarkerStyle colors a path marker so an elided prefix cannot be misread as a
// directory the agent actually named. Read at render time so a live re-theme
// self-heals.
func MarkerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Pink)
}

// TruncateCells clips text to width terminal cells, ANSI-aware.
func TruncateCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(text, width, "")
}

// AgentDisplayName renders a provider ID as the outline's role label.
func AgentDisplayName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		return "Claude"
	case "pi":
		return "Pi"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	case "":
		return "Agent"
	default:
		runes := []rune(provider)
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		return string(runes)
	}
}

// TrimRightCells removes trailing blank cells from a styled line without
// stranding its escapes.
func TrimRightCells(line string) string {
	plain := strings.TrimRight(ansi.Strip(line), " \t")
	if plain == "" {
		return ""
	}
	return ansi.Truncate(line, lipgloss.Width(plain), "")
}

// RenderMarkdownTextBox renders a user turn as a compact speech-colored chat
// bubble. Short turns hug their content; long turns grow and wrap up to the
// available width.
func RenderMarkdownTextBox(content string, width int) string {
	if width < 4 || strings.TrimSpace(content) == "" {
		return ""
	}
	bodyWidth := max(1, width-4)
	rendered := markdown.Render(strings.TrimSpace(content), theme.DefaultTheme)
	var lines []string
	for _, renderedLine := range strings.Split(rendered, "\n") {
		if strings.TrimSpace(ansi.Strip(renderedLine)) == "" {
			continue
		}
		for _, wrapped := range strings.Split(markdown.WrapForViewport(renderedLine, bodyWidth), "\n") {
			wrapped = TrimRightCells(wrapped)
			if strings.TrimSpace(ansi.Strip(wrapped)) != "" {
				lines = append(lines, ansi.Truncate(wrapped, bodyWidth, ""))
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	// Short user turns should read like compact chat bubbles, not a few words
	// floating in a pane-wide empty rectangle. Long turns still grow and wrap
	// up to the available width.
	boxWidth := 4 // two borders plus one quiet cell on each side
	for _, line := range lines {
		boxWidth = max(boxWidth, lipgloss.Width(line)+4)
	}
	boxWidth = min(width, boxWidth)
	_, border := KindIconAndStyle("prompt")
	return TranscriptBox(strings.Join(lines, "\n"), boxWidth, border)
}

// RenderAgentMarkdownBox renders assistant prose into a role-colored box whose
// heading rows come from the outline items rather than rendering their source
// hashes as inert body text. This makes H1-H6 lines real navigation targets
// inside the box and lets fold state hide nested sections without printing a
// duplicate heading list beneath the response.
//
// cursor is the index (in the caller's full item slice) of the selected row, or
// negative for a cursorless render; firstHeadingIndex is the full-slice index
// of headingItems[0]. The returned int is the selected line's offset within the
// box, or -1.
func RenderAgentMarkdownBox(content string, headingItems []Item, cursor, firstHeadingIndex, width int) (string, int) {
	if width < 4 {
		return ansi.Truncate(content, max(0, width), ""), -1
	}
	byLine := make(map[int]struct {
		item  Item
		index int
	}, len(headingItems))
	for i, item := range headingItems {
		byLine[item.MarkdownLine] = struct {
			item  Item
			index int
		}{item: item, index: firstHeadingIndex + i}
	}
	headings := markdown.ExtractHeadings(strings.Split(content, "\n"))
	headingAt := make(map[int]markdown.Heading, len(headings))
	for _, heading := range headings {
		headingAt[heading.Line] = heading
	}

	contentWidth := max(1, width-4)
	sectionVisible := true
	inCodeBlock := false
	selectedContentLine := -1
	lastBlock := ""
	var rendered []string
	appendSpacer := func() {
		if len(rendered) > 0 && rendered[len(rendered)-1] != "" {
			rendered = append(rendered, "")
		}
	}
	appendWrapped := func(line string) {
		for _, wrapped := range strings.Split(markdown.WrapForViewport(line, contentWidth), "\n") {
			wrapped = TrimRightCells(wrapped)
			if strings.TrimSpace(ansi.Strip(wrapped)) != "" {
				rendered = append(rendered, ansi.Truncate(wrapped, contentWidth, ""))
			}
		}
	}
	appendPrefixed := func(prefix, body string) {
		bodyWidth := max(1, contentWidth-lipgloss.Width(prefix))
		wrappedBody := strings.Split(markdown.WrapForViewport(body, bodyWidth), "\n")
		for _, line := range wrappedBody {
			line = TrimRightCells(line)
			rendered = append(rendered, ansi.Truncate(prefix+line, contentWidth, ""))
		}
	}
	blockTransition := func(next string) {
		if lastBlock != "" && lastBlock != next {
			appendSpacer()
		}
		lastBlock = next
	}
	quoteBar := theme.DefaultTheme.Muted.Render("▎ ")
	codeBar := theme.DefaultTheme.Muted.Render("▎ ")
	if theme.ASCIIIcons {
		quoteBar = theme.DefaultTheme.Muted.Render("| ")
		codeBar = quoteBar
	}
	bullet := lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Orange).Bold(true).Render(theme.IconBullet + " ")
	codeStyle := lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green).Background(theme.DefaultTheme.Colors.SubtleBackground)

	for lineNo, raw := range strings.Split(strings.TrimSpace(content), "\n") {
		lineNo++
		if heading, ok := headingAt[lineNo]; ok {
			visible, ok := byLine[lineNo]
			if !ok {
				sectionVisible = false
				continue
			}
			sectionVisible = true
			appendSpacer()
			marker := " "
			if visible.index == cursor {
				marker = theme.IconArrowRightBold
				selectedContentLine = len(rendered)
			}
			icon, style := MarkdownHeadingIcon(heading.Level)
			// Expanded Markdown is already visibly structured, so fold chevrons
			// only add a second indentation column. Keep every numbered heading
			// aligned and use the selection marker/highlight for navigation.
			line := PadIconCell(marker, max(1, lipgloss.Width(theme.IconArrowRightBold))) + " " +
				style.Render(icon) + " " + markdown.StyleInlineMarkdown(heading.Text, theme.DefaultTheme)
			if visible.index == cursor {
				line = widget.HighlightStyle().Render(ansi.Truncate(line, contentWidth, ""))
			}
			appendWrapped(line)
			appendSpacer()
			lastBlock = ""
			continue
		}

		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			if !sectionVisible {
				inCodeBlock = !inCodeBlock
				continue
			}
			if !inCodeBlock {
				blockTransition("code")
				language := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if language != "" {
					appendPrefixed(codeBar, theme.DefaultTheme.Muted.Italic(true).Render(language))
				}
				inCodeBlock = true
			} else {
				inCodeBlock = false
				appendSpacer()
				lastBlock = ""
			}
			continue
		}
		if !sectionVisible {
			continue
		}
		if inCodeBlock {
			appendPrefixed(codeBar, codeStyle.Render(raw))
			continue
		}
		if trimmed == "" {
			appendSpacer()
			lastBlock = ""
			continue
		}

		leftTrimmed := strings.TrimLeft(raw, " \t")
		indent := raw[:len(raw)-len(leftTrimmed)]
		switch {
		case strings.HasPrefix(leftTrimmed, ">"):
			blockTransition("quote")
			quote := strings.TrimSpace(strings.TrimPrefix(leftTrimmed, ">"))
			appendPrefixed(indent+quoteBar, lipgloss.NewStyle().Italic(true).Render(markdown.StyleInlineMarkdown(quote, theme.DefaultTheme)))
		case strings.HasPrefix(leftTrimmed, "- ") || strings.HasPrefix(leftTrimmed, "* "):
			blockTransition("list")
			body := markdown.StyleTodoMarkers(leftTrimmed[2:], theme.DefaultTheme)
			appendPrefixed(indent+bullet, markdown.StyleInlineMarkdown(body, theme.DefaultTheme))
		default:
			blockTransition("paragraph")
			appendWrapped(markdown.StyleStreamingLogLine(raw, &inCodeBlock, theme.DefaultTheme))
		}
	}
	for len(rendered) > 0 && rendered[len(rendered)-1] == "" {
		rendered = rendered[:len(rendered)-1]
	}
	if len(rendered) == 0 {
		return "", -1
	}
	_, border := KindIconAndStyle("assistant")
	box := TranscriptBox(strings.Join(rendered, "\n"), width, border)
	if selectedContentLine >= 0 {
		return box, selectedContentLine + 1 // top border
	}
	return box, -1
}

// TranscriptBox gives conversational turns a role-colored boundary without
// competing with selection, status, or Markdown colors. It is assembled
// line-by-line so ANSI-styled and wide-cell content lands on an exact width.
func TranscriptBox(content string, width int, border lipgloss.Style) string {
	if width < 4 {
		return ansi.Truncate(content, max(0, width), "")
	}
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if theme.ASCIIIcons {
		tl, tr, bl, br, hz, vt = "+", "+", "+", "+", "-", "|"
	}
	innerWidth := width - 2
	lines := []string{border.Render(tl + strings.Repeat(hz, innerWidth) + tr)}
	for _, line := range strings.Split(content, "\n") {
		line = ansi.Truncate(line, max(0, innerWidth-2), "")
		padding := strings.Repeat(" ", max(0, innerWidth-2-lipgloss.Width(line)))
		lines = append(lines, border.Render(vt)+" "+line+padding+" "+border.Render(vt))
	}
	lines = append(lines, border.Render(bl+strings.Repeat(hz, innerWidth)+br))
	return strings.Join(lines, "\n")
}

// StatusIcon renders a row status ("completed"/"failed"/"running") as an icon
// and style.
func StatusIcon(status string) (string, lipgloss.Style) {
	if status == "completed" {
		return theme.IconSuccess, theme.DefaultTheme.Success
	}
	return theme.StatusIconAndStyle(status, theme.DefaultTheme)
}

// MarkdownHeadingIcon renders an H1-H6 level as its numbered circle icon and
// per-level color.
func MarkdownHeadingIcon(level int) (string, lipgloss.Style) {
	colors := theme.DefaultTheme.Colors
	switch max(1, min(6, level)) {
	case 1:
		return theme.IconNumeric1CircleOutline, lipgloss.NewStyle().Foreground(colors.Violet)
	case 2:
		return theme.IconNumeric2CircleOutline, lipgloss.NewStyle().Foreground(colors.Blue)
	case 3:
		return theme.IconNumeric3CircleOutline, lipgloss.NewStyle().Foreground(colors.Cyan)
	case 4:
		return theme.IconNumeric4CircleOutline, lipgloss.NewStyle().Foreground(colors.Green)
	case 5:
		return theme.IconNumeric5CircleOutline, lipgloss.NewStyle().Foreground(colors.Yellow)
	default:
		return theme.IconNumeric6CircleOutline, lipgloss.NewStyle().Foreground(colors.Orange)
	}
}

// HierarchyPrefix indents a row by its outline level.
func HierarchyPrefix(level int) string {
	level = max(1, min(6, level))
	return strings.Repeat("│ ", level-1)
}

// KindIcon is KindIconAndStyle's icon alone.
func KindIcon(kind string) string {
	icon, _ := KindIconAndStyle(kind)
	return icon
}

// ItemIconAndStyle resolves a row's glyph from the whole item, so tool rows
// can be told apart by which tool ran rather than all sharing one wrench.
func ItemIconAndStyle(item Item) (string, lipgloss.Style) {
	if (item.Kind == "tool" || item.Kind == "tools") && item.Tool != "" {
		return theme.ToolIconAndStyle(item.Tool, theme.DefaultTheme)
	}
	return KindIconAndStyle(item.Kind)
}

// KindIconAndStyle renders an outline row kind as its icon and role color.
func KindIconAndStyle(kind string) (string, lipgloss.Style) {
	colors := theme.DefaultTheme.Colors
	switch kind {
	case "prompt":
		return theme.IconChat, lipgloss.NewStyle().Foreground(colors.Violet)
	case "notice":
		return theme.IconBell, lipgloss.NewStyle().Foreground(colors.Pink)
	case "assistant", "heading":
		return theme.IconRobot, lipgloss.NewStyle().Foreground(colors.Green)
	case "tools", "tool":
		return theme.IconTool, lipgloss.NewStyle().Foreground(colors.Cyan)
	case "reasoning":
		return theme.IconLightbulb, lipgloss.NewStyle().Foreground(colors.Yellow)
	case "shell":
		return theme.IconShell, lipgloss.NewStyle().Foreground(colors.Blue)
	default:
		return "", lipgloss.NewStyle()
	}
}

// PadIconCell pads an icon to a fixed cell width so mixed-width glyph sets keep
// columns aligned.
func PadIconCell(icon string, width int) string {
	return icon + strings.Repeat(" ", max(0, width-lipgloss.Width(icon)))
}
