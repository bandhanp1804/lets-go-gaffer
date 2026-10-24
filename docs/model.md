# How the numbers are made

Every figure the app shows is computed in plain Go. This page says how.

## Expected points

Raw form is a poor forecast, so players are ranked by a projection model (`internal/projection`) built from the gameweek-by-gameweek record:

- **points per 90 minutes**, with long memory and shrinkage toward the position norm;
- **minutes**, with short memory (who is starting, who was just dropped);
- **FPL's fixture difficulty** for each match: a blank gameweek is worth nothing and a double counts both matches;
- **the chance of playing**, so a doubtful player is scaled down.

A figure called `xp` is expected points in one gameweek, or averaged over several: the view that shows it says which (`xp_basis`).

## The backtest

`gaffer backtest` (and the Model page) measures the model honestly: for each past gameweek it predicts using only earlier gameweeks and compares with what happened. On this season's data (GW3 to GW7):

| Scorer | Rank correlation | Mean abs. error | Avg. points of its top 20 |
|---|---|---|---|
| Last-three-games form | 0.368 | 2.23 | 3.89 |
| Season average | 0.413 | 2.08 | 4.10 |
| Projection model | **0.534** | **1.88** | **4.76** |

It beats form in every one of the five gameweeks. Settings were tuned and then re-checked walk-forward (tune on earlier gameweeks, score the next unseen one): about 0.52 out of sample against 0.38 for form. **Five gameweeks is a small sample**; rerun the backtest as the season grows.

## Decisions built on it

- **Lineup** (`internal/lineup`): the best XI for the upcoming gameweek, with the captain's points counted twice. It tries every legal formation and is checked against enumeration of every legal XI. A captain you choose must start; the choice never changes who starts.
- **Transfers** (`internal/transfers`): each plan's gain over a horizon of gameweeks minus the 4-point hit for every transfer beyond the free ones. A Wildcard is solved exactly: a kept player costs his selling price.
- **What if?** (`service.WhatIf`): checks your plan against the rules, then uses the same measure as the transfer suggestions, so replaying a suggestion gives identical figures.
- **Chip planner** (`service.Chips`), over the next eight gameweeks:
  - *Triple Captain*: each week, your best captain option's expected points, which is exactly what the chip adds.
  - *Bench Boost*: each week, the expected points of the four players your best XI leaves out.
  - *Free Hit*: each week, how much the best possible squad (the exact solver, with your bank plus your squad's selling prices) would beat your XI by.
  - *Wildcard*: the gain over the next four gameweeks from rebuilding now, or one, two or three weeks later; this equals the Wildcard on the Transfers page.

  A week counts as as good as the best when it is within a toss-up (0.3 points) or a sixth of it. Which chips you have left is exact with a live squad; otherwise it is estimated from the chips you have played, assuming FPL gives a fresh set after GW19 (this assumption is stated on the page).
- **Points** (`service.Points`): each player's points times his multiplier (captain doubled, the bench not counted unless Bench Boost), automatic substitutions once FPL confirms them, minus the transfer hit. A captain who plays no minutes passes the armband to the vice-captain, but only once the gameweek is over. This was checked against FPL's own gameweek points for every finished gameweek of a real team, including ones with automatic substitutions.

## Rules the code enforces

2 goalkeepers, 5 defenders, 5 midfielders, 3 forwards; at most three players from a club; a £100.0m budget to build a squad (your bank plus selling prices to change one); legal formations only; one free transfer a gameweek, banking up to five; a 4-point hit for each extra transfer; Wildcard and Free Hit with no limit and no hit. The selling price of a player is what you paid plus half of any price rise, rounded down.
