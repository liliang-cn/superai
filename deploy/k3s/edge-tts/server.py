"""OpenAI's speech endpoint in front of Microsoft Edge's neural voices.

SuperAI speaks to any text-to-speech service that answers OpenAI's
POST /v1/audio/speech. Edge's voices do not, so this does it for them:
the request's input goes to Edge through edge-tts, and the MP3 comes back as
the response. Nothing is stored and no key is needed; it listens only inside
the cluster.

Built and loaded with deploy/k3s/edge-tts/deploy.sh.

The voice is an Edge voice name (zh-CN-XiaoxiaoNeural). An OpenAI voice name
("alloy") is not one, and gets the default instead of an error, so a caller
configured for OpenAI still speaks.
"""

import os

import edge_tts
from aiohttp import web

DEFAULT_VOICE = os.environ.get("EDGE_TTS_VOICE", "zh-CN-XiaoxiaoNeural")
PORT = int(os.environ.get("PORT", "43540"))
# A cap on what one request may ask to be read, so a runaway caller cannot
# queue minutes of synthesis. SuperAI sends a sentence at a time.
MAX_CHARS = 2000


def rate_of(speed) -> str:
    """OpenAI's speed (0.25 to 4, 1 normal) as Edge's rate ("+20%")."""
    try:
        pct = round((float(speed) - 1.0) * 100)
    except (TypeError, ValueError):
        return "+0%"
    pct = max(-75, min(200, pct))
    return f"{pct:+d}%"


async def speech(request: web.Request) -> web.StreamResponse:
    try:
        body = await request.json()
    except Exception:
        return web.json_response({"error": {"message": "expected a JSON body"}}, status=400)
    text = str(body.get("input") or "").strip()
    if not text:
        return web.json_response({"error": {"message": "input is empty"}}, status=400)
    if len(text) > MAX_CHARS:
        return web.json_response({"error": {"message": f"input is over {MAX_CHARS} characters"}}, status=400)
    voice = str(body.get("voice") or "")
    if "Neural" not in voice:
        voice = DEFAULT_VOICE

    audio = bytearray()
    try:
        async for chunk in edge_tts.Communicate(text, voice, rate=rate_of(body.get("speed", 1))).stream():
            if chunk["type"] == "audio":
                audio.extend(chunk["data"])
    except Exception as e:  # Edge refused or could not be reached
        return web.json_response({"error": {"message": f"edge: {e}"}}, status=502)
    if not audio:
        return web.json_response({"error": {"message": "edge returned no audio"}}, status=502)
    return web.Response(body=bytes(audio), content_type="audio/mpeg")


async def health(_: web.Request) -> web.Response:
    return web.Response(text="ok")


app = web.Application(client_max_size=64 * 1024)
app.router.add_post("/v1/audio/speech", speech)
app.router.add_get("/healthz", health)

if __name__ == "__main__":
    web.run_app(app, port=PORT, access_log=None)
