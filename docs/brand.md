# Brand

## Wordmark

Lowercase `līdza`, set in Inter Bold (700) at 26px with −1px letter
spacing (−1/26 em at other sizes; Inter's optical size 26).

| Mode | Letters | Ground |
|---|---|---|
| Dark | `#eee7e3` | `#241821` |
| Light | `#241821` | `#f6f2ed` |

The ground colours are the palette's (`packs/admin/templates/theme.css`):
`#241821` is the sidebar, `#f6f2ed` the light page.

## Files

`docs/brand/` holds the letters as outlines, so they render the same
without the font installed:

- `lidza-wordmark-dark.svg`, `lidza-wordmark-light.svg`: the wordmark on
  a transparent ground, for dark and light backgrounds.
- `lidza-icon-dark.svg`, `lidza-icon-light.svg`,
  `lidza-icon-mulberry.svg`: `lī` on a rounded tile (radius 22%), for
  favicons, app icons and avatars; the mulberry tile is the accent,
  `oklch(47% 0.13 345)` (`#8b376b`).

- `lidza-social.png`: the 1280×640 social preview card (the wordmark on
  `#241821`), uploaded in the GitHub repository's settings.

The README shows the wordmark, dark or light with the reader's theme.

The admin pack draws the same outline in its sidebar
(`lidza-wordmark` in `packs/admin/templates/partials.html`, coloured by
`currentColor`).

The outlines come from Inter's variable font (OFL) at `wght` 700,
`opsz` 26, shaped with its kerning and tracked −1/26 em.
