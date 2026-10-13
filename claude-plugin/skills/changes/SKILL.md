---
name: changes
description: Summarize what changed on the Burrow NAS recently.
argument-hint: [time span like "48h" or "this week"] [folder] [added|modified|deleted]
disable-model-invocation: true
---

What changed on Burrow: $ARGUMENTS

- A time span becomes `hours`: "today" is the hours since midnight, "this week" is 168, unstated is 24. A library path or folder name becomes `folder`. "new", "edited" or "deleted" becomes `kind`.
- Call `recent_changes` with `limit: 500` so the summary isn't cut short. If exactly 500 come back, say the window held more.
- Summarize instead of dumping rows: counts by kind, then by top-level folder. Call out bulk deletes, large additions, and activity in unexpected places. Then list up to 15 notable individual changes with time and size.
