// Package sample holds the sample papers that papers firestore reset --demo
// writes into each workspace after deleting every document. The definition is
// embedded in the binary and never changes at run time; the papers written
// from it are ordinary papers with nothing marking them as samples.
//
// Keep the samples showing what papers can do: when a block type, a paper
// kind or a markup feature is added, add it to a sample in the same change
// (see "Demo samples" in CLAUDE.md).
package sample

import (
	"embed"
	"encoding/json"
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
	// Icon is the paper's icon; empty leaves the default for its kind.
	Icon string
	Kind model.PaperKind
	// File is the body: Markdown for a blocks paper, an HTML document for
	// an HTML paper, and for a database a JSON Database (its batch).
	File string
	// Parent is the key of the parent entry, or "" for the home.
	Parent string
}

// Entries lists the sample papers. A parent comes before its children, and
// children appear in their parent's body in this order, after its Markdown.
func Entries() []Entry {
	return []Entry{
		{Key: "welcome", Title: "Welcome to papers", Icon: "👋", Kind: model.PaperKindBlocks, File: "welcome.md"},
		{Key: "agents", Title: "Working with AI agents", Icon: "🤖", Kind: model.PaperKindBlocks, File: "agents.md", Parent: "welcome"},
		{Key: "rich", Title: "Diagrams and rich blocks", Icon: "🎨", Kind: model.PaperKindBlocks, File: "rich.md"},
		{Key: "dashboard", Title: "Release dashboard", Kind: model.PaperKindHTML, File: "dashboard.html"},
		{Key: "roadmap", Title: "Product roadmap", Kind: model.PaperKindDatabase, File: "roadmap.json"},
	}
}

// Database is the definition of a sample database: the ops of one batch
// sent to it, as an agent would send them (properties, elements and items,
// named by ref), and the Markdown bodies of some items by their ref.
type Database struct {
	Ops    []json.RawMessage `json:"ops"`
	Bodies map[string]string `json:"bodies"`
}

// ReadDatabase reads a database definition file.
func ReadDatabase(name string) (*Database, error) {
	b, err := Read(name)
	if err != nil {
		return nil, err
	}
	var d Database
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, goerr.Wrap(err, "failed to parse sample database", goerr.V("name", name))
	}
	if len(d.Ops) == 0 {
		return nil, goerr.New("a sample database has no ops", goerr.V("name", name))
	}
	return &d, nil
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
