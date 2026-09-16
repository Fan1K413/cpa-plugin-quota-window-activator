$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = "1"

$compiler = (go env CC).Trim()
if (-not $compiler) {
    throw "A C compiler is required for -buildmode=c-shared. Install MinGW-w64 and put gcc on PATH."
}
$command = ($compiler -split "\s+")[0].Trim('"', "'")
if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
    throw "The C compiler reported by 'go env CC' was not found: $compiler"
}

go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go build -buildvcs=false -trimpath -buildmode=c-shared -o dist/quota-window-activator.dll ./cmd/plugin
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Remove-Item -Force -ErrorAction SilentlyContinue dist/quota-window-activator.h
