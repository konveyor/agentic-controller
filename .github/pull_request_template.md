<!--
## PR Title Prefix

Every **PR Title** should be prefixed with an emoji alias to indicate its type.

- Breaking change: :warning: (`:warning:`)
- Non-breaking feature: :sparkles: (`:sparkles:`)
- Patch fix: :bug: (`:bug:`)
- Docs: :book: (`:book:`)
- Infra/Tests/Other: :seedling: (`:seedling:`)
- No release note: :ghost: (`:ghost:`)

For example, a pull request containing a new feature might look like
`:sparkles: Add agent status reporting`.

Use the **alias** (`:sparkles:`) not the emoji character directly.

## Changelog Fragment

PRs with `:sparkles:`, `:bug:`, or `:warning:` prefixes require a changelog
fragment in `changes/unreleased/`. Create one with:

    make changelog-create NAME=<pr-number>-<short-description> KIND=<kind>

Or copy `changes/template.yaml` to `changes/unreleased/<name>.yaml` and edit it.

For more information, see the Konveyor
[Versioning Doc](https://github.com/konveyor/release-tools/blob/main/VERSIONING.md).
-->
