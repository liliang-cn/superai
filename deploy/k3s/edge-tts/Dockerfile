FROM python:3.12-slim
COPY --from=ghcr.io/astral-sh/uv:0.9 /uv /usr/local/bin/uv
RUN uv pip install --system --no-cache edge-tts==7.* aiohttp==3.*
COPY server.py /app/server.py
USER nobody
EXPOSE 43540
CMD ["python", "/app/server.py"]
