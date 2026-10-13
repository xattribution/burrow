# Burrow for Claude Code

Search, read, share and organize the files on your Burrow from Claude Code, approving each call. The plugin connects Burrow's MCP endpoint and adds five commands.

## Install

1. In Burrow, open **Settings → MCP and API tokens → New token**, pick what it may do, and create it. To use the public address, also tick **Settings → Public port → Also serve MCP**.
2. In Claude Code:
   ```
   /plugin install burrow --marketplace xattribution/burrow
   ```
3. Enter the MCP endpoint (for example `https://share.example.com/mcp`) and the token when asked. The token goes into your system's credential store, not a settings file.

Change either one later with `/plugin configure burrow`. Uninstalling the plugin deletes the stored token. Deleting the token in Burrow cuts access at once.

From a shell: `claude plugin install burrow --marketplace xattribution/burrow`, then `claude plugin configure burrow@burrow` to enter the values.

## Commands

| Command | What it does |
|---|---|
| `/burrow:find <description>` | Finds files from a plain description and lists the best matches |
| `/burrow:share <what> [options]` | Finds the file or folder and makes a link, with expiry, password, download limit or note taken from your words |
| `/burrow:links [path \| all]` | Lists your links, flags the exposed ones, and turns off the ones you pick |
| `/burrow:changes [span] [folder] [kind]` | Summarizes what was added, changed and deleted |
| `/burrow:drop <name>` | Makes an upload-only link so someone can send you files (admins only) |

You don't need the commands. Ask in your own words and Claude uses the Burrow tools directly.

## You approve every call

Claude Code asks before each Burrow tool call. Two layers make sure of it:

- **Burrow** marks each tool set to **Ask** on the token (the default) so Claude Code asks on every call in every permission mode, auto and bypass included, despite any allow rule, and doesn't offer "don't ask again". This is the guarantee.
- **The plugin** adds a hook that asks before every Burrow call, auto mode included. It covers older Burrow versions and tools a token has set to **On**.

In `dontAsk` mode, Claude Code refuses Burrow calls instead of asking.

## What Claude can do

The token decides, under **Settings → MCP and API tokens → Edit** in Burrow:

- **Tools:** each one Off, Ask or On, with presets for read only, read and share, everything, or nothing.
- **Folders:** every folder your account can see, or only the ones you list. Search, reading, links and edits all stay inside them.
- **REST API:** off unless scripts need the token too.

A token never reaches further than the account that made it.

The tools cover searching, listing folders, reading text files, recent changes, links, shared folders and drops (admins), and editing: new folders, writing text files, moving and renaming, and deleting to Recently deleted. Uploading binary files, custom link names and editing an existing link stay in the web UI and the phone app.

Per-token settings and the edit tools need a Burrow version that has them. With an older Burrow, the plugin still asks before every call.

## Developing

```
claude --plugin-dir ./claude-plugin
claude plugin validate . --strict
claude plugin validate ./claude-plugin --strict
```

`--plugin-dir` doesn't prompt for the endpoint and token, so install from a local marketplace (`/plugin marketplace add ./`) to test them. Run `/reload-plugins` after editing a skill.
