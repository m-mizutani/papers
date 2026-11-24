package sample_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/usecase/sample"
)

func TestImages(t *testing.T) {
	md := "![one](images/a.png)\ntext ![again](images/a.png) and ![two](images/b.png)\n" +
		"![remote](https://example.com/c.png) ![absolute](/images/d.png) [link](images/e.png)"
	gt.Equal(t, sample.Images(md), []string{"images/a.png", "images/b.png"})
	gt.A(t, sample.Images("no images here")).Length(0)
}

func TestReadEveryEntry(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range sample.Entries() {
		gt.False(t, seen[e.Key])
		seen[e.Key] = true
		if e.Parent != "" {
			gt.True(t, seen[e.Parent]) // a parent comes before its children
		}
		body := gt.R1(sample.Read(e.File)).NoError(t)
		gt.True(t, len(body) > 0)
		for _, img := range sample.Images(string(body)) {
			gt.R1(sample.Read(img)).NoError(t)
		}
	}
	gt.R1(sample.Read(sample.HomeFile)).NoError(t)
}

func TestReadMissingFile(t *testing.T) {
	_, err := sample.Read("missing.md")
	gt.Error(t, err)
}
