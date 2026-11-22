package firestore

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestSearchDSL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"incident", `"incident"`},
		{`"incident report" owner`, `"incident report" "owner"`},
		// Operators of the query language are matched literally.
		{`-draft OR title:x`, `"-draft" "OR" "title:x"`},
		{`back\slash`, `"back slash"`},
		{"障害対応 手順", `"障害対応" "手順"`},
	}
	for _, c := range cases {
		q := gt.R1(model.ParseSearchQuery(c.raw)).NoError(t)
		gt.Equal(t, searchDSL(q), c.want)
	}
}

func TestSearchDocIDs(t *testing.T) {
	// Entry IDs of one paper share the "{paperID}_" prefix and stay inside
	// the [prefix, paperID+"`") range used by paperSearchRefs.
	paper := model.NewPaperID()
	for _, suffix := range []string{string(model.NewBlockID()), searchTitleEntry} {
		id := string(paper) + "_" + suffix
		gt.True(t, id >= string(paper)+"_")
		gt.True(t, id < string(paper)+"`")
	}
}
