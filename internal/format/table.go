// SPDX-License-Identifier: MIT

package format

import (
	"fmt"
	"strings"
	"text/tabwriter"
)

// RenderTable renders headers and rows as column-aligned text using tabwriter.
func RenderTable(m TableMarshaler) (string, error) {
	headers, rows := tableCells(m)
	if len(headers) == 0 {
		return "", fmt.Errorf("table has no headers")
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))
	rule := make([]string, len(headers))
	for i := range rule {
		rule[i] = "-"
	}
	_, _ = fmt.Fprintln(tw, strings.Join(rule, "\t"))
	for _, row := range rows {
		if len(row) != len(headers) {
			return "", fmt.Errorf("table row has %d columns, want %d", len(row), len(headers))
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// tableCells returns the headers and rows of m with every cell escaped by EscapeLine: a
// cell holds one line, and a tab in it would split the column.
func tableCells(m TableMarshaler) ([]string, [][]string) {
	headers := escapeCells(m.TableHeaders())
	rows := m.TableRows()
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = escapeCells(row)
	}
	return headers, out
}

// escapeCells returns a copy of cells, escaped: the marshaler's slices are not changed.
func escapeCells(cells []string) []string {
	if cells == nil {
		return nil
	}
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = EscapeLine(c)
	}
	return out
}
