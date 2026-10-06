# The world: an apiary

The whole app is one live 3D world, and that world is SuperAI's own: an apiary.
Nothing in it is borrowed from another domain. Each SuperAI concept is a thing
in the apiary that is really there and moves with the data. Each page of the
app is one place in the apiary. Opening a page flies the camera there.

Operations happen in the world:
- **Select a thing, act on it.** The inspector card on the right carries every
  action for the selected thing.
- **Place actions.** The bar along the bottom carries the place's own actions.
- **One line to the queen, from anywhere.** The bar's input talks to her.

A page's old list is still there, behind a "List" button, as a fallback.
Desktop and web draw the world with three.js. iOS draws it with SceneKit.

The look comes from a logistics "digital twin" (WareTrack): light, isometric,
low-poly, clean shadows, white floating cards, pill labels over things,
selection brackets on the ground. Only the look is borrowed; the concepts are
SuperAI's own.

## What is what

| SuperAI | in the apiary | moves when |
|---|---|---|
| **You** | the beekeeper; your cottage stands at the edge | — |
| **The queen** (chat) | the great hive at the centre: a tower of honeycomb with the queen's chamber on top | She is thinking: the chamber glows and bees circle it. A tool call sends a ripple off the top. |
| **Worker** (hive member) | a hive box (stacked supers, a roof, an entrance) on the plot of its node | It has orders: its frames light up, one per order. A lost worker's box goes dark. |
| **Order / delegation** | a forager bee carrying a ball of pollen from the queen's hive to the worker's entrance | It hovers there while working, then flies home carrying honey when done, or turns red when failed. |
| **Bee** (standing agent) | a named bee with a role colour (guard, scout, forager, nurse), flying its own beat over its patch | An awake bee flies its round. A bee waiting for you flies to your cottage door. A paused bee rests on its post. |
| **Linked machines** (Mac's Codex/Claude, sds-b's openclaw) | neighbouring apiaries over the fence, one hive per machine, roofs in their own colour | A coding run is bees working that hive. A failed run is a red bee. |
| **Shared brain** (CortexDB) | the honey store: a wall of comb | Each sealed cell is knowledge. A save is a bee filling a cell. |
| **Skills** | flower beds, one flower clump per skill | — |
| **MCP servers** | distant fields past the fence, with a flight line to each | A connected line is solid; a broken one is grey. |
| **Needs you** | bees waiting at your cottage door, each holding a card | An approval is a bee with a raised flag. A failure holds a red card. |
| **Calendar, notes, people** | the board by your door, a week across it | Each meeting or reminder is pinned on its day. |
| **Dashboards** | wooden signboards around the apiary | — |
| **Stats** | the honey jars and the thermometer by the store | Jars fill with tokens. The thermometer is tok/s. |
| **Settings** | the beekeeper's shed | — |

## Look

| token | value | use |
|---|---|---|
| meadow | `#e8efe0` | ground |
| path | `#efe6d2` | paths between places |
| white | `#fbfbf8` | hive boxes, cottage walls |
| honey | `#f2b416` (deep `#c98a00`) | honey, bees, the queen's chamber, live things |
| wood | `#c99b6b` | roofs, posts, boards |
| leaf | `#5cc98a` | trees, stems |
| petal | `#ffffff`, `#ffd8e0`, `#ffe9a8`, `#cfe3ff` | flowers |
| ink | `#16202b` | labels, bee stripes |
| select | `#2f5bea` | selection brackets and the selected label only |
| alert | `#e2553f` | failed, lost |

- Low-poly with rounded edges; flat colours, lit; soft shadows.
- **Camera:** orthographic and isometric. Drag to pan, right-drag to orbit,
  wheel to zoom. Buttons: + / − / ⟲ / ⟳ / ⌂.
- **Labels:** white pills, with a honey dot when working. The selected label
  turns blue.
- **Motion:** only data moves things, plus wings and gently swaying flowers.
  Reduced motion means no ambient movement.
- Light theme only. No purple.

## Layout

- The world fills the window and the rail stays on the left.
- **Bar along the bottom of the world:**
  - **List**, which shows the page's old list as a panel on the left;
  - the place's actions, e.g. Hive "+ Worker / − Worker", Bees "New bee";
  - the line to the queen.
- **KPI chips:** at the top left of the world.
- **Inspector:** at the top right, for the selected thing, with its actions.
- **Home:** the conversation stays as it is. The live pane is a window onto the
  queen's hive.

## iOS

- The same apiary, drawn with SceneKit: orthographic camera, physically based
  materials, soft shadows.
- **Home:** the apiary, with the cards floating over it.
- **Interaction:** tapping a thing opens its sheet with the same actions.
  Sheets fly the camera to their place.
