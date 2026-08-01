package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitParagraphs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "blank line starts a new paragraph",
			in:   "First para.\n\nSecond para.",
			want: []string{"First para.", "Second para."},
		},
		{
			// Staff typing into a textarea soft-wrap their lines; those must
			// join into one paragraph rather than render as a ragged block.
			name: "single newlines join within a paragraph",
			in:   "One line\nand its continuation.\n\nNext.",
			want: []string{"One line and its continuation.", "Next."},
		},
		{
			name: "windows line endings",
			in:   "First.\r\n\r\nSecond.",
			want: []string{"First.", "Second."},
		},
		{
			name: "runs of blank lines collapse",
			in:   "First.\n\n\n\nSecond.",
			want: []string{"First.", "Second."},
		},
		{
			name: "surrounding whitespace is trimmed",
			in:   "   Padded.   \n\n  Also padded.  ",
			want: []string{"Padded.", "Also padded."},
		},
		{
			name: "whitespace-only input yields nothing",
			in:   "   \n\n  \n",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, splitParagraphs(tt.in))
		})
	}
}
