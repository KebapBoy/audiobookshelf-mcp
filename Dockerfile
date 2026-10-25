FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/abs-mcp .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/abs-mcp /abs-mcp
ENV MCP_TRANSPORT=http \
    MCP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/abs-mcp"]
