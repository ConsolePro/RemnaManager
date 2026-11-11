param(
    [string]$Go = 'go',
    [string]$DockerContext = 'desktop-linux'
)
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskID = [Guid]::NewGuid().ToString('N')
$taskProject = "rmgo-protection-$taskID"
$taskFixture = Join-Path $taskRoot ".test-state/$taskProject"
$taskCompose = Join-Path $taskFixture 'compose.json'
$taskVariables = @('RM_PROTECTION_FIXTURE', 'RM_PROTECTION_NGINX_COOKIE', 'RM_PROTECTION_NGINX_TINYAUTH', 'RM_PROTECTION_CADDY_COOKIE', 'RM_PROTECTION_CADDY_TINYAUTH','RM_PROTECTION_NGINX_MFA','RM_PROTECTION_CADDY_MFA')
$taskPrevious = @{}
foreach ($taskVariable in $taskVariables) { $taskPrevious[$taskVariable] = [Environment]::GetEnvironmentVariable($taskVariable, 'Process') }
Push-Location $taskRoot
try {
    $env:RM_PROTECTION_FIXTURE = $taskFixture
    & $Go test ./internal/installer -run '^TestWriteProtectionFixture$' -count=1
    if ($LASTEXITCODE -ne 0) { throw 'Fixture generation failed' }
    & docker --context $DockerContext compose -p $taskProject -f $taskCompose up -d --wait --wait-timeout 120
    if ($LASTEXITCODE -ne 0) { throw 'Protection fixture startup failed' }
    foreach ($taskProxy in @('nginx', 'caddy')) {
        foreach ($taskMode in @('cookie', 'tinyauth', 'mfa')) {
            $taskPort = & docker --context $DockerContext compose -p $taskProject -f $taskCompose port "$taskProxy-$taskMode" 443
            if ($LASTEXITCODE -ne 0) { throw 'Cannot read test endpoint' }
            [Environment]::SetEnvironmentVariable(('RM_PROTECTION_' + $taskProxy.ToUpper() + '_' + $taskMode.ToUpper()), $taskPort.Trim(), 'Process')
        }
    }
    & $Go test ./internal/installer -run '^TestLivePanelProtection$' -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Protection verification failed' }
    & docker --context $DockerContext compose -p $taskProject -f $taskCompose restart mfa-gateway
    if ($LASTEXITCODE -ne 0) { throw 'MFA restart failed' }
    & docker --context $DockerContext compose -p $taskProject -f $taskCompose up -d --wait --wait-timeout 120
    if ($LASTEXITCODE -ne 0) { throw 'MFA did not become ready after restart' }
    & $Go test ./internal/installer -run '^TestLivePanelProtection$/(nginx|caddy)/mfa$' -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'MFA identities or second factor did not survive restart' }
} finally {
    if (Test-Path -LiteralPath $taskCompose) {
        & docker --context $DockerContext compose -p $taskProject -f $taskCompose down --volumes --remove-orphans
    }
    foreach ($taskVariable in $taskVariables) { [Environment]::SetEnvironmentVariable($taskVariable, $taskPrevious[$taskVariable], 'Process') }
    Pop-Location
}
