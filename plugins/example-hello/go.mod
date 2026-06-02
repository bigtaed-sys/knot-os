// Standalone module: the reference plugin builds independently of the
// core/cli workspace (it is intentionally NOT listed in go.work, so
// `go build ./...` from the repo root doesn't pull it in). Build it
// with: cd plugins/example-hello && GOOS=linux GOARCH=arm64 go build -o example-hello .
module knot-os.plugin.example-hello

go 1.25
