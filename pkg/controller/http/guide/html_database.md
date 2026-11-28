# Connecting HTML to databases (papers.db)

An HTML paper, or an html block, may read and write databases of its workspace. Its scripts call `papers.db`; the papers page around the sandboxed frame checks each call and runs it through the API **as the person viewing the page**. The HTML never holds a token or cookie, and it reaches only the databases you list.

## Listing the databases

List them in `databases`: `[{"id": "<database paper id>", "write": true}]`, at most 10, each once. `write` (default false) lets the HTML set values, add items and edit items' titles and bodies; without it the HTML only reads. Each id must be a database (kind database) of the same workspace.

- New HTML paper: `papers_create_paper` with `kind: "html"`, `html` and `databases`; or `{"op":"insert","type":"paper","paper_kind":"html","html":"...","databases":[...]}`.
- Existing HTML paper: `{"op":"set_html","html":"...","base_rev":N,"databases":[...]}`. Without `databases` the current list stays; `[]` removes it.
- html block: `props.databases` on insert or update (props replace all props, so send `height` and `fields` too).

People see which databases the HTML is connected to, and whether it reads or writes them. When someone views an earlier version, the HTML is not connected (every call fails with `unavailable`).

## The API

`papers` is a frozen global defined before any of your scripts run (do not declare your own `papers`). Every method returns a Promise. Results are the papers REST answers as they are.

```ts
type Value = string | string[] | { user_id: string; name: string }[]
type Database = { id: string; write: boolean; available: boolean; paper: Paper | null }
// Paper: { id, title, icon, kind, properties?: Property[], database_id?, created_at, updated_at, ... }
// Property: { id, name, type: "text"|"date"|"select"|"multi_select"|"person", elements?: { id, value, color }[] }

papers.db.databases(): Promise<Database[]>          // in the order you listed; available=false: not found or not visible
papers.db.items(databaseId: string, query?: {
  filter?: Filter[]; sort?: string; after?: string; limit?: number   // limit 1-100
}): Promise<{ items: { item: Paper; values: PropertyValue[] }[]; next_cursor: string }>
papers.db.setValues(databaseId: string, itemId: string, set: Record<string, Value | null>): Promise<BatchResult>
papers.db.addItem(databaseId: string, item?: { title?: string; icon?: string; after?: string; values?: Record<string, Value> }): Promise<BatchResult>
papers.db.item(itemId: string): Promise<{ paper: Paper; values: PropertyValue[]; blocks: Block[] }>
papers.db.writeItem(itemId: string, ops: Op[], options?: { force?: boolean }): Promise<BatchResult>
papers.db.subscribe(databaseId: string, onEvent: (event: Event) => void): () => void   // returns unsubscribe
// PropertyValue: { paper_id, property_id, value: Value, updated_by, updated_at }
// Block: { id, type, content, props, rev }   BatchResult: { paper, blocks, deleted, refs, items, values }
```

- `filter` and `sort` are those of papers_read on a database: `[{"property":"<id>","op":"is","elements":["<element id>"]}]` and `"<property id>|title:asc|desc"` or `"created|updated:desc"` (created and updated sort newest first only). Values hold element ids for select and multi_select, never the text.
- `addItem` puts the item at the end unless `after` names an item. `values` are set in the same batch.
- `item` returns the item's title, values and all of its blocks with their `rev`.
- `writeItem` takes these ops on the item's body and title: insert (not type paper, html, svg or mermaid, and no `props.databases`), update, delete, move, insert_markdown, set_title, set_icon, insert_row, delete_row, move_row, insert_col, delete_col, move_col. update does not change html, svg or mermaid blocks. update and delete carry `base_rev`; when someone changed the block since, the call fails with `conflict` and `error.conflicts` lists each block's current state: re-read, redo the edit and send again; pass `{ force: true }` only when the person wants their text to win. No version is saved.
- `subscribe` delivers the database's changes: `{type: "item_put", paper}`, `{type: "item_deleted", item_id}`, `{type: "property_value_put", property_value}`, `{type: "property_value_deleted", item_id, property_id}`, `{type: "paper_updated", paper}` (definitions or order changed), and `{type: "resync"}` after a reconnection, when events may have been missed: read again.

## Errors

A failed call rejects with an `Error` whose `code` is one of:

| code | meaning |
|---|---|
| `not_connected` | the database is not listed, or the item is not in a listed database |
| `read_only` | a write to a database listed without `write` |
| `invalid_argument` | a malformed call or an op writeItem does not take (also bad values, from the server) |
| `busy` | more than 100 calls waiting; at most 4 run at once |
| `unavailable` | not connected here (an earlier version is shown, or the frame left the page) |
| `not_found`, `conflict`, `permission_denied`, `internal` | the server's answer |

## Example

A board that moves a card to Done (a select property):

```html
<div id="board"></div>
<script>
(async () => {
  const [db] = await papers.db.databases()
  const status = db.paper.properties.find((p) => p.name === 'Status')
  const done = status.elements.find((e) => e.value === 'Done')
  const draw = async () => {
    const { items } = await papers.db.items(db.id, { limit: 100 })
    const board = document.getElementById('board')
    board.replaceChildren(...items.map(({ item, values }) => {
      const row = document.createElement('button')
      const v = values.find((x) => x.property_id === status.id)
      row.textContent = item.title + (v ? ' (' + status.elements.find((e) => e.id === v.value)?.value + ')' : '')
      if (db.write) row.onclick = () => papers.db.setValues(db.id, item.id, { [status.id]: done.id })
      return row
    }))
  }
  papers.db.subscribe(db.id, draw)
  await draw()
})()
</script>
```

Loading from other hosts follows the workspace's sandbox setting, as for any HTML (see sandbox in papers_guide with workspace_id). Treat values as text: set `textContent`, never `innerHTML`.

The sandbox does not let a `<form>` submit: handle a button's `click` (and Enter in an input, skipping it while `event.isComposing`) instead of `submit`.
