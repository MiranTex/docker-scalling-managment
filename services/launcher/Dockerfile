# Build em duas etapas: a primeira tem toda a toolchain do Go, a segunda só
# carrega o binário já compilado — a imagem final não leva compilador nem
# código-fonte, só o executável. Mesmo padrão de services/templatesadmin/Dockerfile.
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/launcher ./cmd/launcher

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/launcher /usr/local/bin/launcher

ENTRYPOINT ["/usr/local/bin/launcher"]
