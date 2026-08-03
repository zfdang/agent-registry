# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/registry-api .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/registry-api /registry-api
EXPOSE 8080
ENTRYPOINT ["/registry-api"]
