# Builds the release exe into dist\ with Windows version resources, its
# SHA-256 file and the third-party license notices. CI runs the same script.
#   .\build.ps1            vet, test and build
#   .\build.ps1 -SkipTests build only
param([switch]$SkipTests)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

function Invoke-Go {
    & go @args
    if ($LASTEXITCODE -ne 0) { throw "go $($args -join ' ') failed" }
}

$version = (Select-String -LiteralPath main.go -Pattern '^const version = "(.+)"').Matches[0].Groups[1].Value
$asset = 'claude-account-switcher-windows-amd64.exe'
$dist = Join-Path $PSScriptRoot 'dist'
$null = New-Item -ItemType Directory -Force -Path $dist

if (-not $SkipTests) {
    Invoke-Go vet ./...
    Invoke-Go test ./...
}

# Version info and a manifest make an unsigned exe look less like malware to
# Windows Defender. go-winres writes rsrc_windows_amd64.syso, which go build
# links in; the file is generated, so it is not committed.
Invoke-Go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64 --product-version "$version.0" --file-version "$version.0"
Invoke-Go build -trimpath -ldflags '-H=windowsgui -s -w' -o (Join-Path $dist $asset) .

$hash = (Get-FileHash -LiteralPath (Join-Path $dist $asset) -Algorithm SHA256).Hash.ToLowerInvariant()
$utf8 = [System.Text.UTF8Encoding]::new($false)
[System.IO.File]::WriteAllText((Join-Path $dist "$asset.sha256"), "$hash  $asset`n", $utf8)

# The licenses of every module compiled into the exe, read from the module cache.
$notices = [System.Text.StringBuilder]::new("Third-party license notices for $asset $version`n")
$modules = & go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}|{{.Dir}}{{end}}{{end}}' . | Sort-Object -Unique
foreach ($module in $modules) {
    $name, $dir = $module -split '\|', 2
    Get-ChildItem -LiteralPath $dir -File | Where-Object { $_.Name -match '^(LICEN[CS]E|COPYING|NOTICE)' } | Sort-Object Name | ForEach-Object {
        $null = $notices.Append("`n$name - $($_.Name)`n`n").Append([System.IO.File]::ReadAllText($_.FullName)).Append("`n")
    }
}
[System.IO.File]::WriteAllText((Join-Path $dist 'THIRD_PARTY_NOTICES.txt'), $notices.ToString(), $utf8)

Write-Host "Built dist\$asset $version"
Write-Host "SHA-256 $hash"
