# Conversion report

2 files converted, 1 included by others and not converted on their own. 9 items approximated, 6 dropped, 7 to review.

Approximated: converted, but the Markdown looks or behaves differently. Dropped: not in the output. Review: needs a person.

| File | Line | Construct | Action | Detail |
| --- | ---: | --- | --- | --- |
| guide/configure.adoc | 4 | explicit id | approximated | explicit section ids are written as {#id}; the site needs heading attributes (MkDocs attr_list, Hugo's default Markdown, Docusaurus) |
| guide/configure.adoc | 12 | cross reference | review | anchor "missing-anchor" not found in guide/configure.adoc; kept as written |
| guide/configure.adoc | 12 | cross reference text | review | a cross reference without text uses the target's id or file name |
| guide/install.adoc | 2 | author line | approximated | moved to the front matter as author |
| guide/install.adoc | 12 | menu | approximated | menu selections become bold text joined by > |
| guide/install.adoc | 16 | toc | dropped | toc::[] dropped: the site generates its own table of contents |
| guide/install.adoc | 20 | admonition | approximated | admonitions become blockquote alerts; the site needs a blockquote render hook for them to look like admonitions |
| guide/install.adoc | 30 | conditional | approximated | ifdef::product[] is true; lines 30-32 kept without the condition |
| guide/install.adoc | 34 | conditional | dropped | ifndef::product[] is false; lines 34-36 left out |
| guide/install.adoc | 38 | conditional | dropped | ifeval::["{version}" >= "3.0"] is false; lines 38-40 left out |
| guide/install.adoc | 42 | conditional | review | ifeval::["{edition}" == "enterprise"] could not be evaluated; lines 42-44 kept (pass the attribute with --attr) |
| guide/install.adoc | 51 | callouts | approximated | callout markers become (n) in code comments, and the callout list becomes a bullet list of (n) items |
| guide/install.adoc | 72 | image size | dropped | image width, height and alignment are not carried over |
| guide/install.adoc | 87 | table | review | table has merged cells or block content: written as HTML; check it renders on the target site |
| guide/install.adoc | 93 | table | review | table has merged cells or block content: written as HTML; check it renders on the target site |
| guide/install.adoc | 102 | link window | dropped | links that open a new window (^ or window=) open in the same window |
| guide/install.adoc | 114 | sidebar | approximated | sidebars become block quotes |
| guide/install.adoc | 118 | highlight | approximated | highlighted text becomes <mark> |
| guide/install.adoc | 118 | index term | dropped | index entries are dropped; the visible term is kept |
| guide/install.adoc | 118 | superscript | approximated | superscript and subscript become <sup> and <sub> |
| guide/install.adoc | 120 | passthrough | review | passthrough block kept as raw HTML; check it renders on the target site |
| guide/install.adoc | 125 | callouts | review | callout markers removed: the code language has no known comment syntax, so the list no longer points into the code |

## Files

- guide/configure.adoc → guide/configure.md
- guide/install.adoc → guide/install.md
- partials/_snippet.adoc: not converted on its own (included by guide/install.adoc)

## Assets copied

- guide/images/diagram.png
