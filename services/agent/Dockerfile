# O agente dos nós remotos (ver cmd/agent). Compila na arquitetura de quem
# constrói para a do alvo, sem emulação.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
# A versão (commit do git) que o agente reporta à plataforma.
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-X main.version=$VERSION" -o /out/agent ./cmd/agent

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/agent /usr/local/bin/agent
ENTRYPOINT ["/usr/local/bin/agent"]
