Param(
    [string]$InstallDir = "C:\mcp\codebase"
)

# Install latest codebase release for the current Windows system and
# setup configuration under ~/.codebase/config.json (empty file by default)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Write-Info {
    param([string]$Message)
    Write-Host "[INFO] $Message"
}

function Write-Warn {
    param([string]$Message)
    Write-Warning "[WARN] $Message"
}

function Ensure-Admin {
    $currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($currentIdentity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Warn "请以管理员身份运行此脚本，否则无法写入系统环境变量。"
    }
}

function Ensure-Tls12 {
    try {
        $protocols = [Net.ServicePointManager]::SecurityProtocol
        if (($protocols -band [Net.SecurityProtocolType]::Tls12) -eq 0) {
            [Net.ServicePointManager]::SecurityProtocol = $protocols -bor [Net.SecurityProtocolType]::Tls12
        }
    } catch {
        Write-Warn "无法强制设置 TLS1.2，将继续尝试下载。$_"
    }
}

function Get-LatestReleaseAsset {
    param(
        [string]$Owner,
        [string]$Repo
    )

    $apiUrl = "https://api.github.com/repos/$Owner/$Repo/releases/latest"
    $headers = @{ "User-Agent" = "codebase-installer" }

    Write-Info "正在从 GitHub 获取最新发行版信息: $apiUrl"
    $release = Invoke-RestMethod -Uri $apiUrl -Headers $headers

    if (-not $release.assets -or $release.assets.Count -eq 0) {
        throw "该仓库的最新发行版中没有可用的构建资产。"
    }

    $is64 = [Environment]::Is64BitOperatingSystem
    $osTag = "windows"
    $archTag = if ($is64) { "amd64|x86_64|x64" } else { "386|x86" }

    $regex = "(?i)$osTag.*($archTag)"

    $asset = $release.assets | Where-Object { $_.name -match $regex } | Select-Object -First 1

    if (-not $asset) {
        # Fallback: any windows asset
        $asset = $release.assets | Where-Object { $_.name -match "(?i)windows" } | Select-Object -First 1
    }

    if (-not $asset) {
        throw "未找到适用于当前 Windows 系统的构建资产，请检查 GitHub Releases 命名。"
    }

    Write-Info "选择的构建资产: $($asset.name)"
    return $asset
}

function Install-CodebaseBinary {
    param(
        [string]$InstallDir,
        [object]$Asset
    )

    $headers = @{ "User-Agent" = "codebase-installer" }

    if (-not (Test-Path $InstallDir)) {
        Write-Info "创建安装目录: $InstallDir"
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    $downloadPath = Join-Path $env:TEMP $Asset.name
    Write-Info "下载构建到临时文件: $downloadPath"
    Invoke-WebRequest -Uri $Asset.browser_download_url -Headers $headers -OutFile $downloadPath

    $ext = [IO.Path]::GetExtension($downloadPath)

    if ($ext -eq ".zip") {
        Write-Info "检测到 ZIP 归档，正在解压到安装目录。"
        Expand-Archive -LiteralPath $downloadPath -DestinationPath $InstallDir -Force
    } else {
        Write-Info "直接复制构建文件到安装目录。"
        $targetPath = Join-Path $InstallDir $Asset.name
        Copy-Item -Path $downloadPath -Destination $targetPath -Force
    }

    Remove-Item -Path $downloadPath -ErrorAction SilentlyContinue

    Write-Info "安装完成，目录: $InstallDir"
}

function Ensure-SystemEnvironment {
    param(
        [string]$InstallDir
    )

    Ensure-Admin

    # Set CODEBASE_HOME
    Write-Info "设置系统环境变量 CODEBASE_HOME=$InstallDir"
    [Environment]::SetEnvironmentVariable("CODEBASE_HOME", $InstallDir, "Machine")

    # Add to PATH if not already present
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $separator = ";"

    $pathItems = $machinePath -split [IO.Path]::PathSeparator
    if ($pathItems -notcontains $InstallDir) {
        Write-Info "将 $InstallDir 添加到系统 PATH 中。"
        if ([string]::IsNullOrWhiteSpace($machinePath)) {
            $newPath = $InstallDir
        } else {
            $newPath = $machinePath.TrimEnd($separator) + $separator + $InstallDir
        }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "Machine")
    } else {
        Write-Info "系统 PATH 已包含 $InstallDir，无需修改。"
    }

    Write-Info "系统环境变量更新完成（可能需要重新打开终端生效）。"
}

function Setup-ConfigFile {
    $configDir = Join-Path $env:USERPROFILE ".codebase"
    $configPath = Join-Path $configDir "config.json"

    if (-not (Test-Path $configDir)) {
        Write-Info "创建配置目录: $configDir"
        New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    }

    if (-not (Test-Path $configPath)) {
        # 默认创建一个空的配置文件，用户可按需自行填充
        New-Item -ItemType File -Path $configPath -Force | Out-Null
        Write-Info "已创建空配置文件: $configPath"
    } else {
        Write-Info "配置文件已存在: $configPath"
    }
}

try {
    Write-Info "开始安装 codebase 到 $InstallDir"

    Ensure-Tls12

    $asset = Get-LatestReleaseAsset -Owner "sxueck" -Repo "codebase"
    Install-CodebaseBinary -InstallDir $InstallDir -Asset $asset

    Ensure-SystemEnvironment -InstallDir $InstallDir

    Setup-ConfigFile

    Write-Info "全部步骤完成。"
} catch {
    Write-Error "安装失败: $_"
    exit 1
}

exit 0
