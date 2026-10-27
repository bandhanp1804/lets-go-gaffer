# Web UI

A Next.js (React, TypeScript) app in `web/`, on port 3001, that talks to the Go API. Start the API (`gaffer serve`), then `cd web && npm install && npm run dev` and open http://127.0.0.1:3001.

## Choosing a team

The landing page and the team chip in the navigation open a dialog. Paste your FPL team id (or the whole address of your Points page, which contains it) and, optionally, switch on **Live squad** to include transfers made since the deadline.

A live squad needs your login token, and the dialog explains how to get it:

- **A short script** you can read first, run in the console of your own FPL tab. It reads the token the page is already using, from values the tab has stored and from the requests it makes when you click Transfers, and prints it in the console. It draws nothing on the page (a text box there could be read by browser extensions) and sends nothing anywhere. It must not be followed by a page reload, which removes it. Its logic is tested against a stand-in page, but it has not been run against FPL's real site, which needs a login.
- **By hand** from the Network tab, which always works.

The team id is remembered in this browser. The token lives in `sessionStorage`, so it is gone when the tab closes, and it expires after about an hour anyway.

## Screens

| Screen | What it shows |
|---|---|
| **Home** | A soccer-themed landing page with a stadium scoreboard of the season status. |
| **My team** | Your squad on a pitch with availability flags, the captain and vice-captain, and the points the team is scoring in the gameweek being played (provisional until the matches finish). Click a player to see where his points came from, match by match, as FPL's own panel shows them. Points come from FPL's public live feed and your public picks, so they need no token and describe the team you fielded even after you have made transfers for next week. |
| **Best XI** | Close calls first, then the pitch and the changes from your current lineup. The formation chips are buttons that show that formation's lineup. **Choose your captain and vice-captain** from the table of starters, which shows each one's expected points, what he is worth as captain and what choosing him gives up; a captain must start, and the choice never changes who starts. Under it, **When to play your chips**: for each chip, whether this week is fine, which week to wait for, or that none stands out, with a bar per gameweek (doubles marked 2×). Triple Captain follows the captain you chose. |
| **Transfers** | Ranked moves with hit, net gain and bank, Wildcard planning and what-if free transfers. Each suggestion opens in What if?. |
| **What if?** | Try your own transfers: click a player of yours to sell him, pick a replacement, and the plan is weighed in plain Go. It checks the rules (like for like, at most three from a club, real selling prices against your bank) and says what it is worth after any points hit, who would start or sit on the bench, whether the captain changes, risk flags, your free transfers next week, and a clearly better pick for the slot when one is affordable. It uses the same measure as Transfers, so a suggestion replayed here gives identical numbers (a test enforces it). |
| **Squad builder** | The best legal fifteen from scratch under £100m. |
| **Players** | Every player, filtered and sorted; paged on the server (up to 100 a page) with a stable order. |
| **News** | Semantic search over stored stories. Name a player in your question and it looks at stories about him (it says so, and offers "Not him? Search all news"); if nothing is stored about him it says that, with FPL's own status for him. Each story shows how strong a match it is, and the page says how old the stored news is, and a **Refresh news** button pulls the feeds again now (the server also does it on a timer; see `GAFFER_NEWS_REFRESH`). Each story links to its source, and the page names any feed the last pull could not read. |
| **Ask** | The assistant, streamed as it is written, with sources, the checks' warnings and the tools it used ([assistant.md](assistant.md)). |
| **Model** | The backtest, including the out-of-sample check ([model.md](model.md)), and **Model health**: the nightly eval's pass rate and the default model's backtest after each sync. |
| **Sign out** | Behind the login, the nav shows a **Sign out** link (hover for who you are). It signs out on production, whose login cookie previews share, and lands on the login again. Not shown on your own computer, where there is no login. |
| **Setup** | Your default team and FPL login token, saved for your sign-in so any device loads them: the token's time left, **Refresh token** to paste a fresh one (with the capture script), and Remove. Both are encrypted on the server with a key the database never holds, and the token is never sent back to a browser. Without a key on the server, the team is remembered in the browser as before. |
| **Alerts** | The inbox: watched players' news and status changes, the gameweek briefing, and model-health warnings, with an unread count in the nav. Star (☆) a player on Players or News to watch him. Alerts are produced by the background worker ([temporal.md](temporal.md)). |

## Design notes

- **No CORS and no keys in the browser.** `next.config.mjs` rewrites `/api/*` to the Go server (`GAFFER_API`, default `http://127.0.0.1:8080`), so the browser only talks to its own origin and the API's origin and host guard still applies through the proxy.
- **Answers are rendered from text, never HTML.** `lib/markdown.ts` turns the assistant's markdown into data and React builds elements from it, so nothing the model (or a news story it read) writes can inject markup or a script link. Only `http(s)` addresses become links.
- **Few dependencies.** Next, React and TypeScript, pinned exactly. No UI kit, chart library or CSS framework: plain CSS, light and dark.
- **Types come from Go.** `web/lib/types.gen.ts` is generated; do not edit it by hand ([development.md](development.md)).
- **Tests.** `npm test` runs Node's built-in test runner over the pure logic (the SSE parser, pitch grouping, the markdown reader, team-id and token parsing, the token script, paging); `npm run typecheck` checks types.

## Building without disturbing a running dev server

`GAFFER_DIST=.next-test npx next build` writes to its own folder, so a build never touches the one `next dev` is using.
