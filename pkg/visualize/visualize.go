// Package visualize renders deterministic SVG diagrams of Terraform state address graphs
// and the transitions between two versions of a module.
package visualize

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

// Options controls SVG rendering.
type Options struct {
	Title  string
	Width  int
	Height int
	Theme  string // "light" or "dark"
}

func (o Options) normalized() Options {
	if o.Width <= 0 {
		o.Width = 1200
	}
	if o.Height <= 0 {
		o.Height = 800
	}
	if o.Theme != "dark" {
		o.Theme = "light"
	}
	if o.Title == "" {
		o.Title = "Terraform State Graph"
	}
	return o
}

// RenderBefore renders the state address graph for the old module version.
func RenderBefore(old *ast.ModuleInfo, opts Options) ([]byte, error) {
	opts = opts.normalized()
	addrs := sortedAddresses(old)
	return renderSingle(addrs, opts, "Before")
}

// RenderAfter renders the state address graph for the new module version.
func RenderAfter(new *ast.ModuleInfo, opts Options) ([]byte, error) {
	opts = opts.normalized()
	addrs := sortedAddresses(new)
	return renderSingle(addrs, opts, "After")
}

// RenderTransition renders an overlay of old and new address graphs with shift edges.
func RenderTransition(old, new *ast.ModuleInfo, shifts []ast.ResourceAddressShift, opts Options) ([]byte, error) {
	opts = opts.normalized()
	oldAddrs := sortedAddresses(old)
	newAddrs := sortedAddresses(new)

	// Map old full address -> new full address for migrated/shifted pairs.
	shiftMap := make(map[string]string)
	for _, sh := range shifts {
		shiftMap[sh.OldAddress.FullAddress()] = sh.NewAddress.FullAddress()
	}

	rows := len(oldAddrs)
	if len(newAddrs) > rows {
		rows = len(newAddrs)
	}
	if rows == 0 {
		rows = 1
	}
	rowH := 26
	margin := 40
	panelW := (opts.Width - 3*margin) / 2
	height := opts.Height
	if need := rows*rowH + 2*margin + 40; need > height {
		height = need
	}

	bg, fg, _ := palette(opts.Theme)
	var b strings.Builder
	writeHeader(&b, opts, height, bg, fg)

	// Panel titles.
	b.WriteString(fmt.Sprintf(`<text x="%d" y="30" font-family="monospace" font-size="16" fill="%s" font-weight="bold">Before</text>`, margin, fg))
	b.WriteString(fmt.Sprintf(`<text x="%d" y="30" font-family="monospace" font-size="16" fill="%s" font-weight="bold">After</text>`, margin*2+panelW, fg))

	leftX := margin
	rightX := margin*2 + panelW
	startY := 60

	oldIndex := make(map[string]int, len(oldAddrs))
	for i, a := range oldAddrs {
		oldIndex[a.FullAddress()] = i
	}
	newIndex := make(map[string]int, len(newAddrs))
	for i, a := range newAddrs {
		newIndex[a.FullAddress()] = i
	}

	// Draw connectors first so nodes paint on top.
	for _, sh := range shifts {
		oi, ok1 := oldIndex[sh.OldAddress.FullAddress()]
		ni, ok2 := newIndex[sh.NewAddress.FullAddress()]
		if !ok1 || !ok2 {
			continue
		}
		y1 := startY + oi*rowH + 14
		y2 := startY + ni*rowH + 14
		color := "#58a6ff"
		dash := "stroke-dasharray=\"6,4\""
		if !sh.IsRecreation {
			dash = ""
		}
		b.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="2" %s marker-end="url(#arrow)"/>`, leftX+panelW, y1, rightX, y2, color, dash))
	}
	// Unmatched old nodes: draw a short "removed" stub.
	for i, a := range oldAddrs {
		if _, ok := shiftMap[a.FullAddress()]; ok {
			continue
		}
		if _, ok := newIndex[a.FullAddress()]; ok {
			// retained: straight connector
			j := newIndex[a.FullAddress()]
			y1 := startY + i*rowH + 14
			y2 := startY + j*rowH + 14
			b.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#8b949e" stroke-width="1.5" marker-end="url(#arrow)"/>`, leftX+panelW, y1, rightX, y2))
		}
	}

	// Draw nodes.
	for i, a := range oldAddrs {
		y := startY + i*rowH
		color := "#8b949e"
		if _, ok := newIndex[a.FullAddress()]; !ok {
			if newAddr, shifted := shiftMap[a.FullAddress()]; shifted && newAddr != "" {
				color = "#58a6ff"
			} else {
				color = "#f85149"
			}
		}
		drawNode(&b, leftX, y, panelW, 20, color, fg, truncate(a.FullAddress(), 46))
	}
	for i, a := range newAddrs {
		y := startY + i*rowH
		color := "#8b949e"
		// added if not in old
		found := false
		for _, o := range oldAddrs {
			if o.FullAddress() == a.FullAddress() {
				found = true
				break
			}
		}
		if !found {
			// shifted target?
			isShiftedTarget := false
			for _, sh := range shifts {
				if sh.NewAddress.FullAddress() == a.FullAddress() && sh.OldAddress.FullAddress() != a.FullAddress() {
					isShiftedTarget = true
				}
			}
			if isShiftedTarget {
				color = "#58a6ff"
			} else {
				color = "#3fb950"
			}
		}
		drawNode(&b, rightX, y, panelW, 20, color, fg, truncate(a.FullAddress(), 46))
	}

	writeFooter(&b)
	return []byte(b.String()), nil
}

func renderSingle(addrs []ast.ResourceAddress, opts Options, label string) ([]byte, error) {
	rowH := 26
	margin := 40
	width := opts.Width
	height := opts.Height
	if need := len(addrs)*rowH + 2*margin + 40; need > height {
		height = need
	}
	bg, fg, _ := palette(opts.Theme)
	var b strings.Builder
	writeHeader(&b, opts, height, bg, fg)
	b.WriteString(fmt.Sprintf(`<text x="%d" y="30" font-family="monospace" font-size="16" fill="%s" font-weight="bold">%s</text>`, margin, fg, label))
	startY := 60
	panelW := width - 2*margin
	for i, a := range addrs {
		y := startY + i*rowH
		drawNode(&b, margin, y, panelW, 20, "#58a6ff", fg, truncate(a.FullAddress(), 80))
	}
	writeFooter(&b)
	return []byte(b.String()), nil
}

func sortedAddresses(mod *ast.ModuleInfo) []ast.ResourceAddress {
	if mod == nil {
		return nil
	}
	addrs := ast.ExtractResourceAddresses(mod)
	sort.Slice(addrs, func(i, j int) bool {
		return addrs[i].FullAddress() < addrs[j].FullAddress()
	})
	return addrs
}

func drawNode(b *strings.Builder, x, y, w, h int, fill, fg, text string) {
	b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="4" fill="%s" opacity="0.85"/>`, x, y, w, h, fill))
	b.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-family="monospace" font-size="12" fill="%s">%s</text>`, x+6, y+14, fg, escapeXML(text)))
}

func writeHeader(b *strings.Builder, opts Options, height int, bg, fg string) {
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, opts.Width, height, opts.Width, height))
	b.WriteString(fmt.Sprintf(`<rect width="100%%" height="100%%" fill="%s"/>`, bg))
	b.WriteString(fmt.Sprintf(`<title>%s</title>`, escapeXML(opts.Title)))
	b.WriteString(`<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#8b949e"/></marker></defs>`)
}

func writeFooter(b *strings.Builder) {
	b.WriteString(`</svg>`)
}

func palette(theme string) (bg, fg, muted string) {
	if theme == "dark" {
		return "#0d1117", "#e6edf3", "#8b949e"
	}
	return "#ffffff", "#1f2328", "#57606a"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func escapeXML(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
