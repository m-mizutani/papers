package markup_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/markup"
)

type markupCase struct {
	In         string `json:"in"`
	Normalized string `json:"normalized"`
	Plain      string `json:"plain"`
	Error      bool   `json:"error"`
}

// The same cases run in frontend/src/markup/markup.test.ts so that the
// server and the editor agree on every spelling.
func loadCases(t *testing.T) []markupCase {
	t.Helper()
	data, err := os.ReadFile("../../../testdata/markup/cases.json")
	gt.NoError(t, err).Required()
	var cases []markupCase
	gt.NoError(t, json.Unmarshal(data, &cases)).Required()
	return cases
}

func TestNormalizeSharedCases(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.In, func(t *testing.T) {
			got, err := markup.Normalize(c.In)
			if c.Error {
				gt.Error(t, err)
				return
			}
			gt.NoError(t, err)
			gt.Equal(t, got, c.Normalized)
			gt.Equal(t, markup.PlainText(c.In), c.Plain)

			// canonical text is a fixed point and keeps the same plain text
			again := gt.R1(markup.Normalize(got)).NoError(t)
			gt.Equal(t, again, got)
			gt.Equal(t, markup.PlainText(got), c.Plain)
		})
	}
}

func TestPaletteRejectsUnknownColours(t *testing.T) {
	_, err := markup.Parse("[x]{.teal}")
	gt.Error(t, err)
	gt.True(t, len(markup.Palette) == 9)
}
