# The headless serve binary. No Wails GUI dependencies: CGO is off, and the SPA
# is embedded, so the image is one static binary plus a seed script.
FROM node:22-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
ENV GOWORK=off CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/frontend/dist ./frontend/dist
RUN go build -trimpath -ldflags "-s -w" -o /out/superai . \
 && go build -trimpath -ldflags "-s -w" -o /out/superai-daemon ./cmd/superai-daemon \
 && go build -trimpath -ldflags "-s -w" -o /out/superai-hive-worker ./cmd/superai-hive-worker

FROM alpine:3.21 AS kubectl
ARG TARGETARCH
ARG KUBECTL_VERSION=v1.36.2
RUN apk add --no-cache curl \
 && curl -fsSL -o /kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
 && chmod +x /kubectl

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata su-exec \
 && adduser -D -u 10001 superai
COPY --from=go /out/ /usr/local/bin/
# In a pod kubectl needs no kubeconfig: it reads the ServiceAccount token
# mounted at /var/run/secrets, so what the agent may do is exactly what the
# pod's Role says (deploy/k3s/superai-hive.yaml).
COPY --from=kubectl /kubectl /usr/local/bin/kubectl
COPY deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV SUPERAI_HOME=/data SUPERAI_NO_BROWSER=1
VOLUME /data
EXPOSE 43117
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["superai", "serve", "-bind", "0.0.0.0", "-port", "43117"]
