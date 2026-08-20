FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/keenetic-policy-switcher ./cmd/keenetic-policy-switcher

FROM scratch
COPY --from=build /out/keenetic-policy-switcher /keenetic-policy-switcher
EXPOSE 8080
ENTRYPOINT ["/keenetic-policy-switcher"]
