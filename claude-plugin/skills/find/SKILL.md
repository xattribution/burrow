---
name: find
description: Find files on Burrow from a plain-language description.
argument-hint: <what you're looking for>
disable-model-invocation: true
---

Find on Burrow: $ARGUMENTS

Turn the description into `search_files` calls:

- Keep the distinctive nouns, names, years and extensions. Drop filler like "my", "the file with", "from". "the STL for the shapeoko spindle bracket" becomes `query: "spindle bracket ext:stl"`.
- Dates ("last month", "since March") go in `modified_after`. "Biggest" is `sort: largest`; "latest" or "recent" is `sort: newest`.
- When the wording is ambiguous, run two or three variants at once (synonyms, singular and plural, abbreviations) and merge the results.
- No hits: drop the least distinctive word and retry, then list the likely library with `list_directory`.

Reply with a compact table of path, size and modified, best match first, at most 15 rows, and say how many more matched. If one result is clearly the one, say so. If the user wants to see a text file, read it with `read_text_file`.
