# Frontend build stage
FROM node:22-alpine AS build-frontend
WORKDIR /app/frontend
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm run build

# Go build stage
FROM golang:1.26-alpine AS build-go
ENV CGO_ENABLED=0
ARG BUILD_VERSION=dev
RUN apk add --no-cache git
WORKDIR /app
ENV GOCACHE=/root/.cache/go-build
ENV GOMODCACHE=/root/.cache/go-mod
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && go mod verify
COPY . /app
COPY --from=build-frontend /app/frontend/dist /app/frontend/dist
RUN --mount=type=cache,target=/root/.cache/go-mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=readonly -ldflags="-w -s -X main.version=${BUILD_VERSION}" -o papers

# Final stage
FROM gcr.io/distroless/base:nonroot
USER nonroot
COPY --from=build-go /app/papers /papers
ENTRYPOINT ["/papers"]
CMD ["serve"]
