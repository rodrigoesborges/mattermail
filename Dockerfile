FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo docker)" -o /mattermail .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /mattermail /mattermail
USER nonroot
ENTRYPOINT ["/mattermail"]
CMD ["-config", "/etc/mattermail/mattermail.toml"]
