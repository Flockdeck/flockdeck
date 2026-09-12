# Install Flockdeck on Windows, from PowerShell:
#
#   irm https://flockdeck.ai/install.ps1 | iex
#
# It downloads the release archive for this machine from dl.flockdeck.ai, or
# from GitHub, which carries every release too, when that cannot be reached;
# checks it against the release's checksums.txt from the same place; and puts
# flockdeck.exe in
# %LOCALAPPDATA%\Programs\flockdeck. That is a directory the user owns, which
# matters later: the application updates itself in place, and it should never
# need elevation to do it. The directory is added to the user's PATH, and the
# Start menu gets a shortcut.
#
# Settings, all optional, read from the environment:
#
#   FLOCKDECK_VERSION         a release tag such as v0.2.8; the latest by default.
#                             To stay on it, also set FLOCKDECK_UPDATE=off for
#                             your user, or Flockdeck updates itself to the latest.
#   FLOCKDECK_INSTALL_DIR     where flockdeck.exe goes
#   FLOCKDECK_DOWNLOAD        a mirror to fetch the release files from instead,
#                             laid out as <mirror>/<version>/<file>; the latest
#                             is read from <mirror>/latest.json, or asked of GitHub
#   FLOCKDECK_NO_MODIFY_PATH  set to 1 to leave PATH and the Start menu alone
#
# The archive names below are the ones cmd/release writes, and the updater in
# internal/selfupdate reads. The three have to agree.
#
# Everything is inside one function, which is called on the last line, so a
# download cut short halfway through defines nothing that runs.

function Install-Flockdeck {
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell draws a progress bar for every download, and drawing
    # it makes the download many times slower.
    $ProgressPreference = 'SilentlyContinue'
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'jmwri/flockdeck'
    # Where releases are found: dl.flockdeck.ai first, then GitHub. The tests
    # point these at servers of their own.
    $dl = 'https://dl.flockdeck.ai'
    $github = 'https://github.com'
    $githubApi = 'https://api.github.com'

    # The machine's own architecture, from the registry rather than from the
    # process: an x64 PowerShell on an ARM64 machine would otherwise install
    # the emulated build.
    $native = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment').PROCESSOR_ARCHITECTURE
    switch ($native) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "flockdeck: no release is built for $native" }
    }

    # A mirror given by hand is the only place files are fetched from.
    if ($env:FLOCKDECK_DOWNLOAD) {
        $primary = $env:FLOCKDECK_DOWNLOAD.TrimEnd('/')
        $fallback = $null
    } else {
        $primary = $dl
        $fallback = "$github/$repo/releases/download"
    }

    $version = $env:FLOCKDECK_VERSION
    if (-not $version) {
        # latest.json names the latest release, and holds nothing else:
        # {"version":"v1.2.3"}. Anything that does not read as a release
        # version is refused rather than downloaded.
        try {
            $resp = Invoke-WebRequest -UseBasicParsing -TimeoutSec 30 "$primary/latest.json"
            $text = if ($resp.Content -is [byte[]]) { [Text.Encoding]::UTF8.GetString($resp.Content) } else { $resp.Content }
            $version = ($text | ConvertFrom-Json).version
        } catch {
            $version = $null
        }
        if ($version -notmatch '^v\d+\.\d+\.\d+') {
            if ($fallback) {
                Write-Host "flockdeck: could not reach $primary; downloading from GitHub instead"
                $primary = $fallback
                $fallback = $null
            }
            try {
                $version = (Invoke-RestMethod -UseBasicParsing "$githubApi/repos/$repo/releases/latest").tag_name
            } catch {
                throw "flockdeck: could not find the latest release; set FLOCKDECK_VERSION to choose one"
            }
            if (-not $version) { throw "flockdeck: GitHub's answer did not name a release" }
        }
    }
    # Releases are tagged v1.2.3, and a version is as often written without
    # the v; either finds the release rather than a download that is not there.
    if ($version -notmatch '^v') { $version = "v$version" }

    $dir = if ($env:FLOCKDECK_INSTALL_DIR) { $env:FLOCKDECK_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\flockdeck' }
    $dest = Join-Path $dir 'flockdeck.exe'
    $archive = "flockdeck_${version}_windows_$arch.zip"

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('flockdeck-' + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "flockdeck: downloading $archive"
        # The archive and its checksums come from one place, so the one is
        # always checked against that place's own copy of the other. A release
        # from before dl.flockdeck.ai is only on GitHub, as is everything while
        # the site cannot be reached. A version that was never released is the
        # usual failure here, and Invoke-WebRequest's own error does not say
        # which file it wanted.
        $failure = $null
        foreach ($base in @($primary, $fallback) | Where-Object { $_ }) {
            try {
                foreach ($name in $archive, 'checksums.txt') {
                    $failure = "could not download $base/$version/$name"
                    Invoke-WebRequest -UseBasicParsing "$base/$version/$name" -OutFile (Join-Path $tmp $name)
                }
                $failure = $null
                break
            } catch {
                $failure = "$failure ($($_.Exception.Message))"
                if ($fallback -and $base -ne $fallback) {
                    Write-Host "flockdeck: could not download it from $base; downloading it from GitHub instead"
                }
            }
        }
        if ($failure) { throw "flockdeck: $failure" }

        $want = $null
        foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -ge 2 -and $fields[1] -eq $archive) { $want = $fields[0] }
        }
        if (-not $want) { throw "flockdeck: checksums.txt for $version does not list $archive" }
        # The archive is hashed and opened with .NET rather than Get-FileHash
        # and Expand-Archive. Those two live in script modules that PowerShell
        # loads on first use, from PSModulePath. A Windows PowerShell started
        # from PowerShell 7 inherits a PSModulePath that finds PowerShell 7's
        # copies of those modules first, which it cannot load, and then the
        # command is simply not found: GitHub's Windows runner is exactly that.
        $stream = [IO.File]::OpenRead((Join-Path $tmp $archive))
        try { $sum = [Security.Cryptography.SHA256]::Create().ComputeHash($stream) } finally { $stream.Dispose() }
        $got = -join ($sum | ForEach-Object { $_.ToString('x2') })
        if ($got -ne $want) { throw "flockdeck: $archive does not match its published checksum; nothing was installed" }

        $unpacked = Join-Path $tmp 'unpacked'
        # Windows PowerShell has to be told to load the assembly ZipFile is in;
        # PowerShell 7 has it already, and may not know it by this name.
        try { Add-Type -AssemblyName System.IO.Compression.FileSystem } catch { }
        [IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $tmp $archive), $unpacked)
        if (-not (Test-Path (Join-Path $unpacked 'flockdeck.exe'))) { throw "flockdeck: $archive has no flockdeck.exe in it" }

        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        # flockdeck-chat.exe is the console twin an API agent's pane runs,
        # because flockdeck.exe itself gets no console there. Releases before it
        # have none, so it goes in when the archive has one and is not missed
        # when it does not. The archive's checksum covers both.
        foreach ($name in 'flockdeck.exe', 'flockdeck-chat.exe') {
            $from = Join-Path $unpacked $name
            if (-not (Test-Path $from)) { continue }
            $to = Join-Path $dir $name
            # Windows will not overwrite an executable that is running, but it
            # will rename one. This is what the application's own updater does:
            # the old file goes to <name>.old, and its next start removes it.
            Copy-Item $from "$to.new" -Force
            if (Test-Path $to) {
                # The last .old can itself still be running, when the installer
                # is run a second time before the app has been restarted.
                # Windows will not delete a running program but will rename one,
                # so what is in the way is set aside under a name of its own.
                $old = "$to.old"
                if (Test-Path $old) {
                    try { Remove-Item $old -Force } catch { $old = "$to.$([Guid]::NewGuid().ToString('N')).old" }
                }
                Move-Item $to $old
            }
            Move-Item "$to.new" $to
        }
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
    Write-Host "flockdeck: installed $version to $dest"
    if ($env:FLOCKDECK_VERSION) {
        Write-Host "flockdeck: to stay on $version, set FLOCKDECK_UPDATE=off for your user; otherwise Flockdeck updates itself to the latest"
    }
    # Starting it again while an older copy runs joins that copy instead.
    Write-Host 'flockdeck: if Flockdeck is already running, quit it before starting this version'

    # A copy found first on PATH -- a go install, say -- is the one that
    # `flockdeck` runs, and adding this directory to the end of PATH will not
    # change that.
    $run = 'flockdeck'
    $found = Get-Command flockdeck -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found -and $found.Source -ne $dest) {
        Write-Host "flockdeck: note: the flockdeck your shell finds first is $($found.Source), not this one"
        $run = "& '$dest'"
    }

    if ($env:FLOCKDECK_NO_MODIFY_PATH -eq '1') { return }

    # PATH is read and written through the registry, unexpanded, because
    # [Environment]::SetEnvironmentVariable would store it expanded and turn
    # every %USERPROFILE% already in it into one fixed path.
    $key = Get-Item 'HKCU:\Environment'
    $path = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
    $parts = @($path -split ';' | Where-Object { $_ })
    if ($parts -notcontains $dir) {
        $kind = if ($path -match '%') { 'ExpandString' } else { 'String' }
        Set-ItemProperty 'HKCU:\Environment' -Name Path -Value (($parts + $dir) -join ';') -Type $kind
        # Writing the registry tells nobody. Setting and clearing a variable
        # through .NET broadcasts the change, so a terminal opened from the
        # Start menu from now on sees the new PATH.
        [Environment]::SetEnvironmentVariable('FLOCKDECK_INSTALLING', '1', 'User')
        [Environment]::SetEnvironmentVariable('FLOCKDECK_INSTALLING', $null, 'User')
        $env:Path = "$env:Path;$dir"
        Write-Host "flockdeck: added $dir to your PATH"
    }

    try {
        $link = Join-Path ([Environment]::GetFolderPath('Programs')) 'Flockdeck.lnk'
        $shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut($link)
        $shortcut.TargetPath = $dest
        $shortcut.WorkingDirectory = $env:USERPROFILE
        $shortcut.Save()
        Write-Host 'flockdeck: added Flockdeck to the Start menu'
    } catch {
        Write-Host "flockdeck: could not add a Start menu shortcut: $_"
    }
    Write-Host "flockdeck: start it from the Start menu, or run: $run"
}

Install-Flockdeck
