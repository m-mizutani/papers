# Frontend build stage
FROM node:26-alpine AS build-frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
# Node.js no longer bundles corepack; install the pnpm that packageManager names
RUN npm install -g "$(node -p "require('./package.json').packageManager")"
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
# shared test cases, read by the type-checked markup tests
COPY testdata/markup/ /app/testdata/markup/
RUN pnpm run build

# Go build stage
FROM golang:1.27-alpine AS build-go
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
