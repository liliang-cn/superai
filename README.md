# SuperAI

Your own AI assistant. Talk to it from your phone, your computer or a browser. It remembers what you tell it, keeps your schedule and reminds you, and can put the coding tools on your machines to work for you.

![Ask the queen in the 3D view; the answer streams into her speech bubble](docs/media/world.gif)

| Chat | iPhone |
|---|---|
| ![Answers stream in with tables and lists](docs/media/chat.gif) | ![Today, the schedule and the 3D view on the phone](docs/media/iphone.gif) |

## What's in it

- **Core** — the assistant itself. It runs on one machine you leave on, and everything else talks to it.
- **Apps** — the web page the core serves, the desktop app for macOS and Windows, and the iPhone app.
- **Agents** — install SuperAI on your other machines and the core can use Claude Code, Codex and the other coding tools there.
- **Hive** — for more power, run several cores together: one queen takes your request and splits it, and workers do the parts at the same time.

## How to use it

1. **Start the core.** Download SuperAI from [Releases](https://github.com/liliang-cn/superai/releases) and run `superai serve`, or open the desktop app.
2. **Sign in.** Open the web page and log in with the password it printed the first time it started.
3. **Choose a model.** In Settings, sign in with your Claude, Codex or Gemini account, or enter an API key.
4. **Add your phone.** In Settings, make a pairing code and enter it in the iPhone app.
5. **Talk to it.** Ask questions, give it to-dos, set reminders and alarms, or ask it to get something done on one of your machines.
