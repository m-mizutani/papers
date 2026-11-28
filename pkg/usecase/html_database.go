package usecase

import (
	"context"
	"slices"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// An HTML document (an HTML paper's html_page block, or an html block) may
// name databases of its workspace in props.databases. In the viewer's
// browser, the page around its sandboxed frame then reads and writes those
// databases for the document's scripts (papers.db), as the person viewing
// it and through the ordinary API; the server only keeps and checks the
// list (model D24).

// checkDatabaseLinks validates an HTML document's database connections:
// their shape, and that each database not already in known is a database
// of the workspace and not in the trash. A trashed paper is seen only by
// its creator, so it is refused like a missing one for everybody, which
// tells no one that it exists; a link the document already has stays.
// The reads are not part of the transaction (u.repo, as for images).
func (u *UseCases) checkDatabaseLinks(ctx context.Context, ws model.WorkspaceID, links, known []model.DatabaseLink) error {
	if err := model.ValidateDatabaseLinks(links); err != nil {
		return err
	}
	var ids []model.PaperID
	for _, l := range links {
		if !slices.ContainsFunc(known, func(k model.DatabaseLink) bool { return k.ID == l.ID }) {
			ids = append(ids, l.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	papers, err := u.repo.GetPapers(ctx, ws, ids)
	if err != nil {
		return goerr.Wrap(err, "failed to read the connected databases")
	}
	found := map[model.PaperID]bool{}
	for _, p := range papers {
		if p != nil && p.IsDatabase() && !p.IsTrashed() {
			found[p.ID] = true
		}
	}
	for _, id := range ids {
		if !found[id] {
			return goerr.Wrap(model.ErrInvalidArgument, "databases must name databases of this workspace", goerr.V("id", id))
		}
	}
	return nil
}

// currentDatabaseLinks reads the links of an HTML paper's document as they
// are now (its html_page block), outside any transaction.
func (u *UseCases) currentDatabaseLinks(ctx context.Context, paper *model.Paper) ([]model.DatabaseLink, error) {
	blocks, err := u.repo.ListBlocks(ctx, paper.WorkspaceID, paper.ID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the html_page block", goerr.V("paper_id", paper.ID))
	}
	page := pageOf(blocks)
	if page == nil {
		return nil, nil
	}
	return page.Props.Databases, nil
}
