# Building the documentation

Tenderdash uses [mdBook](https://rust-lang.github.io/mdBook/), like the Dash
Platform book. The site includes the guides in `docs/`, all Markdown specifications
in `spec/`, and the RPC reference from `rpc/openapi/`.

## Local build

Install Python 3.10 or later and mdBook **0.4.40** (the version used by Platform):

```sh
cargo install mdbook --version 0.4.40 --locked
make build-docs
```

Run `make serve-docs` and open `http://localhost:3000/`. To serve an existing
build, run `python3 -m http.server 8000 --directory book/book` and open
`http://localhost:8000/`. Use HTTP so the RPC reference can fetch its schema.
`MDBOOK=/path/to/mdbook make build-docs` selects a particular binary.
For guides and specifications, edit their canonical files in `docs/` or `spec/`,
then run the build again. When using the server, rerun `make prepare-docs` after
editing canonical files; mdBook watches the staged copies.

`book/SUMMARY.md` defines the navigation. Add every new Markdown page there;
the build fails if a source page is missing from the summary or a summary entry
has no source file. The HTML build also rejects missing local pages or assets.
The consensus protocol and all its companion chapters are
listed explicitly under Protocol specifications.

The preparation script copies sources and assets into ignored `book/src/`, strips
VuePress metadata, resolves directory links, and points links to repository source
files at the build's Git revision. It also stages the RPC reference at `api/rpc/`.
It does not switch branches or modify documentation sources. Both `book/src/` and
`book/book/` are disposable build output. Set `DOCS_SOURCE_REF` to override the
Git revision used for source links.

## Diagrams and search

Use fenced `mermaid` blocks for diagrams. The book uses the same vendored
Mermaid 11.6.0 distribution as Platform; its MIT license is in
`book/mermaid-LICENSE`. Diagrams render locally without a CDN. mdBook provides
local full-text search without an external search service.

## Preview and publishing

`.github/workflows/docs-deployment.yml` builds pull requests and uploads a
`documentation-preview` artifact. Download it and extract it, then run
`python3 -m http.server 8000` from the extracted directory and open
`http://localhost:8000/`. The RPC reference requires HTTP serving.
Pull requests have read-only repository permissions and cannot deploy.

Pushes to the repository's default branch publish the same output using GitHub
Pages artifacts and the `github-pages` environment, like Platform. This follows
default-branch changes without editing the workflow.

To publish another branch manually, open the workflow in **Actions**, choose
**Run workflow**, and select the branch. The workflow must exist on the default
branch for GitHub to show that control. In **Settings → Pages**, select **GitHub
Actions** as the build source. The `github-pages` environment's deployment rules
must permit the default branch and any branches selected for manual publication.
The workflow reports the deployed URL. Ordinary pushes to other branches do not
publish; pull requests remain previews.

## Naming and attribution

Use Tenderdash for this product, its CLI, and current source links. Preserve
copyright notices, original research titles and citations, external project names,
and identifiers required by the implementation, such as `tendermint.abci`, JSON
public-key type names, metric names, and TLA+/Ivy module names. Do not rename wire
types to change their display name.

Generate the ABCI API page from its protobuf source with `make proto-doc`;
`spec/abci++/api.md` is generated and must not be edited by hand.
