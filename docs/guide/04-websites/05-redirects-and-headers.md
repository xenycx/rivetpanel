---
title: Redirects and headers
lede: Add redirects, rewrites and response headers to a static site with _redirects and _headers files in the Netlify and Cloudflare Pages formats.
icon: shuffle
glance:
  - Up to 1,000 rules per file
  - Checked when a release is published
related: [websites/publishing, websites/bot-sites]
---

## How the files work

Put a `_redirects` file and a `_headers` file at the top of a static site. They
use the [Netlify](https://docs.netlify.com/routing/redirects/) and Cloudflare
Pages formats.

- The files are checked when a release is published and applied when it is
  served.
- A mistake refuses the release and names the file and line
  [enforced: panel].
- The files themselves are never served.
- Each file holds up to 1,000 rules [enforced: panel].

App sites set their own headers and redirects; these files do not apply to
them.

## _redirects

Each line is `from  to  [status][!]`:

```
# _redirects: from  to  [status][!]
/old-page        /new-page              301
/blog/:slug      /posts/:slug           302
/docs/*          https://docs.example.com/:splat
/app/*           /app/index.html        200
/gone            /410.html              410
```

| Status | Effect |
|---|---|
| 301 (default), 302, 303, 307, 308 | Redirect |
| 200 | Serve another file of the site without changing the address (a rewrite; no proxying to other servers) |
| 404, 410 | Serve the target file with that status |

### Matching

- `:name` matches one path segment. A trailing `*` matches the rest, available
  as `:splat`.
- The first matching rule wins.
- A file that exists at the requested path wins over a rule, so
  `/* /index.html 200` only catches unknown paths. To make the rule win
  anyway, end the status with `!`, as in `301!`.

## _headers

A path line, then indented `Name: value` lines:

```
# _headers: a path line, then indented "Name: value" lines
/*
  X-Frame-Options: DENY
  Permissions-Policy: camera=()
/assets/*
  Cache-Control: public, max-age=31536000, immutable
```

Every matching block applies, and later blocks override earlier ones.
`_headers` can override the default caching described in
[Static responses](01-bot-sites.md#static-responses).

Headers that would break the listener or sessions are refused
[enforced: panel]: `Set-Cookie`, hop-by-hop headers, `Content-Length`,
`Content-Encoding`, `ETag`, `Location` and the like.
