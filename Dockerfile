# One image for the dashboard, API and worker; Compose picks the command per service.

FROM node:22-alpine AS web
WORKDIR /web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci
COPY apps/web/ ./
RUN npm run build

FROM golang:1.23-alpine AS go
WORKDIR /src
COPY services/go.mod services/go.sum ./
RUN go mod download
COPY services/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ignition-api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ignition-worker ./cmd/worker

FROM alpine:3.20
# git is used by the worker for shallow clones (invoked directly, never through a shell).
RUN apk add --no-cache git ca-certificates tzdata
COPY --from=go /out/ /usr/local/bin/
COPY --from=web /web/dist /srv/web
ENV IGNITION_WEB_DIR=/srv/web
EXPOSE 8080
CMD ["ignition-api"]
