// Package sample holds the sample papers that demo mode (papers serve
// --demo) writes into a workspace that is still empty. The definition is
// embedded in the binary and never changes at run time; the papers written
// from it are ordinary papers with nothing marking them as samples.
//
// Keep the samples showing what papers can do: when a block type, a paper
// kind or a markup feature is added, add it to a sample in the same change
// (see "Demo samples" in CLAUDE.md).
package sample

import (
	"embed"
	"io/fs"
	"regexp"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

//go:embed files
var files embed.FS

// HomeFile is the Markdown written into the body of the workspace's home.
const HomeFile = "home.md"

// Entry is one sample paper.
type Entry struct {
	// Key names the entry so that other entries can be its children.
	Key   string
	Title string
	Kind  model.PaperKind
	// File is the body: Markdown for a blocks paper, an HTML document for
	// an HTML paper.
	File string
	// Parent is the key of the parent entry, or "" for the home.
	Parent string
}

// Entries lists the sample papers. A parent comes before its children, and
// children appear in their parent's body in this order, after its Markdown.
func Entries() []Entry {
	return []Entry{
		{Key: "welcome", Title: "Welcome to papers", Kind: model.PaperKindBlocks, File: "welcome.md"},
		{Key: "agents", Title: "Working with AI agents", Kind: model.PaperKindBlocks, File: "agents.md", Parent: "welcome"},
		{Key: "rich", Title: "Diagrams and rich blocks", Kind: model.PaperKindBlocks, File: "rich.md"},
		{Key: "dashboard", Title: "Release dashboard", Kind: model.PaperKindHTML, File: "dashboard.html"},
	}
}

// Read returns a file of the definition by its path, such as "welcome.md"
// or "images/overview.png".
func Read(name string) ([]byte, error) {
	b, err := fs.ReadFile(files, "files/"+name)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read sample file", goerr.V("name", name))
	}
	return b, nil
}

// reImage finds Markdown images whose URL is a relative path: they name a
// file of the definition, stored as an attachment of the paper.
var reImage = regexp.MustCompile(`!\[[^\]]*\]\((images/[^)\s]+)\)`)

// Images lists the image files a Markdown body refers to, in order and
// without duplicates.
func Images(markdown string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reImage.FindAllStringSubmatch(markdown, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
