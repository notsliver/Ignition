# One command to install or update IGNITION on Windows:  .\install.ps1
# Safe to re-run: existing secrets and data are kept.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$compose = @('compose', '--env-file', '.env', '-f', 'deploy/docker-compose.yml')

function Fail($message) { Write-Host $message -ForegroundColor Red; exit 1 }

# 1. Docker must be installed and running.
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Fail "Docker isn't installed. Get Docker Desktop from https://docs.docker.com/get-docker/" }
docker info *> $null
if ($LASTEXITCODE -ne 0) { Fail "Docker isn't running. Start Docker Desktop and try again." }

# 2. Create .env with random secrets the first time.
function RandomBase64([int]$bytes) {
  $buffer = New-Object byte[] $bytes
  [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($buffer)
  [Convert]::ToBase64String($buffer)
}
docker volume inspect ignition_db-data *> $null
if (-not (Test-Path .env) -and $LASTEXITCODE -eq 0) { Fail ".env is missing but an IGNITION database already exists. Restore your .env (it holds the database password and secret key), or remove the old data with: docker compose -f deploy/docker-compose.yml down -v" }
if (-not (Test-Path .env)) {
  Write-Host 'Creating .env with random secrets...'
  $pg = (RandomBase64 24) -replace '[/+=]', ''
  $key = RandomBase64 32
  (Get-Content .env.example) `
    -replace '^POSTGRES_PASSWORD=.*', "POSTGRES_PASSWORD=$pg" `
    -replace '^IGNITION_SECRET_KEY=.*', "IGNITION_SECRET_KEY=$key" |
    Set-Content -Encoding ascii .env
  Write-Host "Back up IGNITION_SECRET_KEY in .env. Without it, stored secrets can't be decrypted." -ForegroundColor Yellow
} else {
  Write-Host 'Using existing .env'
}
$port = ((Get-Content .env | Where-Object { $_ -match '^IGNITION_PORT=' }) -replace '^IGNITION_PORT=', '')
if (-not $port) { $port = '8080' }

# 3. Build and start. Deployed apps share the "ignition" network with the domain proxy.
docker network inspect ignition *> $null
if ($LASTEXITCODE -ne 0) { docker network create ignition | Out-Null }
Write-Host 'Building and starting IGNITION (the first build takes a few minutes)...'
docker @compose up -d --build
if ($LASTEXITCODE -ne 0) { Fail 'docker compose failed.' }

# 4. Wait until the API is healthy.
Write-Host -NoNewline 'Waiting for IGNITION to be ready'
$ready = $false
for ($i = 0; $i -lt 60; $i++) {
  if ((docker inspect -f '{{.State.Health.Status}}' ignition-api-1 2>$null) -eq 'healthy') { $ready = $true; break }
  Write-Host -NoNewline '.'; Start-Sleep -Seconds 2
}
Write-Host ''
if (-not $ready) { Fail "IGNITION didn't become healthy. Check: docker $($compose -join ' ') logs api" }

# 5. Tell the user what to do next.
$code = (docker @compose logs api 2>$null | Select-String -Pattern '"setup_code":"([A-F0-9]+)"' | Select-Object -Last 1).Matches.Groups[1].Value
Write-Host ''
Write-Host "IGNITION is running at http://localhost:$port" -ForegroundColor Green
if ($code) {
  Write-Host "Setup code: $code" -ForegroundColor Green
  Write-Host 'Open the URL, enter the code, and create your owner account.'
} else {
  Write-Host 'Sign in with your existing account.'
}
Write-Host "Stop it with:  docker $($compose -join ' ') down   (your data is kept)"
