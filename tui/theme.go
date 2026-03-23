package tui

import (
	"hash/fnv"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	ColorPrimary   = lipgloss.Color("#7C3AED")
	ColorSecondary = lipgloss.Color("#06B6D4")
	ColorSuccess   = lipgloss.Color("#10B981")
	ColorWarning   = lipgloss.Color("#F59E0B")
	ColorError     = lipgloss.Color("#EF4444")
	ColorMuted     = lipgloss.Color("#6B7280")
	ColorText      = lipgloss.Color("#E5E7EB")
	ColorSubtle    = lipgloss.Color("#374151")
	ColorBg        = lipgloss.Color("#111827")
)

var styles = struct {
	Header     lipgloss.Style
	StatusBar  lipgloss.Style
	LogInfo    lipgloss.Style
	LogWarn    lipgloss.Style
	LogError   lipgloss.Style
	LogDebug   lipgloss.Style
	LogForward lipgloss.Style
	LogTrace   lipgloss.Style
	Service    lipgloss.Style
	Spinner    lipgloss.Style
	Muted      lipgloss.Style
	Bold       lipgloss.Style
	Viewport   lipgloss.Style
}{
	Header:     lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary),
	StatusBar:  lipgloss.NewStyle().Foreground(ColorText).Background(ColorSubtle).Padding(0, 1),
	LogInfo:    lipgloss.NewStyle().Foreground(ColorSecondary),
	LogWarn:    lipgloss.NewStyle().Foreground(ColorWarning),
	LogError:   lipgloss.NewStyle().Bold(true).Foreground(ColorError),
	LogDebug:   lipgloss.NewStyle().Foreground(ColorMuted),
	LogForward: lipgloss.NewStyle().Foreground(ColorText),
	LogTrace:   lipgloss.NewStyle().Foreground(ColorMuted).Italic(true),
	Service:    lipgloss.NewStyle().Bold(true).Foreground(ColorSuccess),
	Spinner:    lipgloss.NewStyle().Foreground(ColorPrimary),
	Muted:      lipgloss.NewStyle().Foreground(ColorMuted),
	Bold:       lipgloss.NewStyle().Bold(true).Foreground(ColorText),
	Viewport:   lipgloss.NewStyle(),
}

// Styles returns the shared style set.
func Styles() *struct {
	Header     lipgloss.Style
	StatusBar  lipgloss.Style
	LogInfo    lipgloss.Style
	LogWarn    lipgloss.Style
	LogError   lipgloss.Style
	LogDebug   lipgloss.Style
	LogForward lipgloss.Style
	LogTrace   lipgloss.Style
	Service    lipgloss.Style
	Spinner    lipgloss.Style
	Muted      lipgloss.Style
	Bold       lipgloss.Style
	Viewport   lipgloss.Style
} {
	return &styles
}

// --- Render helpers: hide lipgloss from callers ---

// RenderHeader renders text as a level-1 or level-2 header.
func RenderHeader(level int, text string) string {
	switch level {
	case 1:
		return lipgloss.NewStyle().Foreground(ColorText).Render(text)
	case 2:
		return styles.Header.Render(text)
	default:
		return text
	}
}

// RenderWarning renders text in the warning style with an icon.
func RenderWarning(text string) string {
	return styles.LogWarn.Render("⚠️ " + text)
}

// RenderTrace renders text in the trace style.
func RenderTrace(text string) string {
	return styles.LogTrace.Render(text)
}

// RenderDebug renders text in the debug style.
func RenderDebug(text string) string {
	return styles.LogDebug.Render(text)
}

// RenderInfo renders text in the info style.
func RenderInfo(text string) string {
	return styles.LogInfo.Render(text)
}

// RenderError renders text in the error style with an icon.
func RenderError(text string) string {
	return styles.LogError.Render("☠️ " + text)
}

// RenderErrorDetail renders text in the error style without an icon.
func RenderErrorDetail(text string) string {
	return styles.LogError.Render(text)
}

// RenderFocus renders text with a rounded border for emphasis.
func RenderFocus(text string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorError).
		Border(lipgloss.RoundedBorder()).
		Render(text)
}

// RenderWithMargin renders text with a standard display margin.
func RenderWithMargin(text string) string {
	return lipgloss.NewStyle().Margin(1, 2, 1, 2).Render(text)
}

// RenderMarkdown renders markdown content with the given glamour style
// (e.g. "dark", "light", "auto").
func RenderMarkdown(content string, style string) (string, error) {
	return glamour.Render(content, style)
}

// --- Service color picker: deterministic per-service styling ---

var serviceColors = []lipgloss.Color{
	lipgloss.Color("#ADD8E6"), lipgloss.Color("#90EE90"),
	lipgloss.Color("#FFC0CB"), lipgloss.Color("#E6E6FA"),
	lipgloss.Color("#00FF00"), lipgloss.Color("#00FFFF"),
	lipgloss.Color("#FF1493"), lipgloss.Color("#7DF9FF"),
	lipgloss.Color("#FF69B4"), lipgloss.Color("#C0C0C0"),
	lipgloss.Color("#FFD700"), lipgloss.Color("#FF4500"),
	lipgloss.Color("#9370DB"), lipgloss.Color("#3CB371"),
	lipgloss.Color("#20B2AA"), lipgloss.Color("#DDA0DD"),
	lipgloss.Color("#B0E0E6"), lipgloss.Color("#FF6347"),
	lipgloss.Color("#4682B4"), lipgloss.Color("#D2691E"),
	lipgloss.Color("#FFDAB9"), lipgloss.Color("#7B68EE"),
	lipgloss.Color("#BA55D3"), lipgloss.Color("#F0E68C"),
	lipgloss.Color("#48D1CC"), lipgloss.Color("#FFB6C1"),
	lipgloss.Color("#DEB887"), lipgloss.Color("#AFEEEE"),
	lipgloss.Color("#98FB98"), lipgloss.Color("#FFA07A"),
	lipgloss.Color("#E0FFFF"), lipgloss.Color("#D8BFD8"),
	lipgloss.Color("#FFDAB9"), lipgloss.Color("#CD853F"),
	lipgloss.Color("#FFA500"), lipgloss.Color("#F0FFF0"),
	lipgloss.Color("#F5DEB3"), lipgloss.Color("#FAFAD2"),
	lipgloss.Color("#B0C4DE"), lipgloss.Color("#FF00FF"),
}

var (
	serviceStyleCache = map[string]func(string) string{}
	serviceStyleMu    sync.RWMutex
)

func hashString(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// ServiceRenderer returns a render function that applies a deterministic
// color to text based on the service's unique identifier. Results are cached.
func ServiceRenderer(unique string) func(string) string {
	serviceStyleMu.RLock()
	if fn, ok := serviceStyleCache[unique]; ok {
		serviceStyleMu.RUnlock()
		return fn
	}
	serviceStyleMu.RUnlock()

	parts := strings.Split(unique, "/")
	var style lipgloss.Style
	if len(parts) < 2 {
		style = lipgloss.NewStyle()
	} else {
		hash := hashString(unique)
		fg := serviceColors[hash%uint32(len(serviceColors))]
		style = lipgloss.NewStyle().Foreground(fg)
	}

	fn := func(s string) string { return style.Render(s) }

	serviceStyleMu.Lock()
	serviceStyleCache[unique] = fn
	serviceStyleMu.Unlock()

	return fn
}

// ServiceFocusRenderer returns a render function like ServiceRenderer but
// with a rounded border added, for focus-level log messages.
func ServiceFocusRenderer(unique string) func(string) string {
	parts := strings.Split(unique, "/")
	var style lipgloss.Style
	if len(parts) < 2 {
		style = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	} else {
		hash := hashString(unique)
		fg := serviceColors[hash%uint32(len(serviceColors))]
		style = lipgloss.NewStyle().Foreground(fg).Border(lipgloss.RoundedBorder())
	}
	return func(s string) string { return style.Render(s) }
}
