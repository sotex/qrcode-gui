<#
.SYNOPSIS
    编译并打包二维码生成器 (walk GUI, Windows/amd64)。
.DESCRIPTION
    1) 用 rsrc 把 app.manifest 与 qrcode.ico 重新嵌入 rsrc.syso（决定 exe 的 Windows 图标与 DPI/视觉样式）。
    2) go build 生成静态链接的 qrcode-gui.exe（CGO_ENABLED=0，跨平台也可在 Linux/macOS 上交叉编译出 Windows exe）。
    3) 把 exe 打包成 dist/qrcode-gui.zip。
.PARAMETER OutputName
    输出 exe 文件名，默认 qrcode-gui.exe。
.PARAMETER NoPackage
    跳过生成 zip 打包步骤，仅编译。
.EXAMPLE
    powershell -ExecutionPolicy Bypass -File build.ps1
#>
[CmdletBinding()]
param(
    [string]$OutputName = 'qrcode-gui.exe',
    [switch]$NoPackage
)

$ErrorActionPreference = 'Stop'

# 项目根目录（脚本所在目录）
$Root = $PSScriptRoot
if (-not $Root) { $Root = (Get-Location).Path }
Set-Location $Root

# ---- 0) 前置检查 ----
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "未找到 go 命令，请先安装 Go 并加入 PATH。"
}
if (-not (Test-Path app.manifest)) { throw "缺少 app.manifest" }
if (-not (Test-Path qrcode.ico))   { throw "缺少 qrcode.ico（程序图标）" }

$OutDir  = Join-Path $Root 'dist'
$ExePath = Join-Path $OutDir $OutputName

# ---- 1) 资源：manifest + 图标 嵌入 rsrc.syso ----
# 资源 ID 约定（与 main.go 的 appIconResID 对应）：rsrc 按声明顺序分配 ID，
# 1=RT_MANIFEST(app.manifest)，2=RT_GROUP_ICON(qrcode.ico)。
$rsrc = Join-Path $Root 'rsrc.exe'
if (-not (Test-Path $rsrc)) {
    $gopath = & go env GOPATH
    $rsrc = Join-Path $gopath 'bin\rsrc.exe'
    if (-not (Test-Path $rsrc)) {
        Write-Host "未找到 rsrc，尝试 go install github.com/akavel/rsrc@latest ..."
        & go install github.com/akavel/rsrc@latest
        $gopath = & go env GOPATH
        $rsrc = Join-Path $gopath 'bin\rsrc.exe'
    }
}
if (-not (Test-Path $rsrc)) { throw "无法获取 rsrc 工具（用于嵌入图标/清单）" }

Write-Host "==> 生成资源 rsrc.syso (manifest + 图标) ..."
& $rsrc -manifest app.manifest -ico qrcode.ico -o rsrc.syso
if ($LASTEXITCODE -ne 0) { throw "rsrc 生成资源失败" }

# ---- 2) 版本信息（通过 -ldflags -X 注入 main.version / main.buildTime）----
$version = "dev"
$gitDesc = & git describe --tags --always --dirty 2>$null
if ($LASTEXITCODE -eq 0 -and $gitDesc) { $version = $gitDesc.Trim() }
# 注意：不能含空格，否则 -X 注入的值会被链接器按空格拆成多个参数而报错。
$buildTime = Get-Date -Format "yyyy-MM-ddTHH:mm:ss"
# -H windowsgui：链接为 Windows GUI 子系统，运行时不弹出控制台窗口。
$ldflags = "-s -w -H windowsgui -X main.version=$version -X main.buildTime=$buildTime"

# ---- 3) 编译（静态链接，无 CGO 依赖）----
$env:CGO_ENABLED = '0'
$env:GOOS        = 'windows'
$env:GOARCH      = 'amd64'

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
Write-Host "==> 编译 $OutputName (version=$version, build=$buildTime) ..."
& go build -ldflags $ldflags -o $ExePath .
if ($LASTEXITCODE -ne 0) { throw "go build 失败" }

$sizeKB = [math]::Round((Get-Item $ExePath).Length / 1KB, 1)
Write-Host "    产物: $ExePath ($sizeKB KB)"

# ---- 4) 打包 ----
if (-not $NoPackage) {
    $zip = Join-Path $OutDir 'qrcode-gui.zip'
    if (Test-Path $zip) { Remove-Item $zip -Force }
    Compress-Archive -Path $ExePath -DestinationPath $zip -Force
    $zipKB = [math]::Round((Get-Item $zip).Length / 1KB, 1)
    Write-Host "==> 已打包: $zip ($zipKB KB)"
}

Write-Host "构建完成。" -ForegroundColor Green
