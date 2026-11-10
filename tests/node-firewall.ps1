$ErrorActionPreference = 'Stop'
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$testBinary = Join-Path $repoRoot '.tools/node-provision.test'
if (-not (Test-Path -LiteralPath $testBinary)) { throw 'Build the Linux installer test binary at .tools/node-provision.test first.' }
$suffix = [guid]::NewGuid().ToString('N')
$prefix = "rmgo-node-fw-$suffix"
$fixture = Join-Path $repoRoot ".test-state/$prefix"
if (Test-Path -LiteralPath $fixture) { throw 'Use a fresh firewall fixture directory.' }
New-Item -ItemType Directory -Path $fixture | Out-Null
$networkID = $null
function Invoke-Docker {
    param([string[]]$DockerArgs)
    $result = & docker @DockerArgs
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $($DockerArgs[0])" }
    return $result
}
try {
    $networkID = (Invoke-Docker -DockerArgs @('network','create',$prefix)).Trim()
    foreach ($role in @('target','panel','other')) {
        $runArgs = @('run','-d','--cidfile',"$fixture/$role.cid",'--name',"$prefix-$role",'--network',$prefix,'--memory','256m','--cpus','1','-v',"${repoRoot}:/source:ro",'-v',"${fixture}:/fixture")
        if ($role -eq 'target') { $runArgs += @('--cap-add','NET_ADMIN','--sysctl','net.ipv4.ip_forward=1') }
        $runArgs += @('--entrypoint','python3','remnamanager-firewall-test:local','/source/tests/node-firewall-fixture.py','serve')
        Invoke-Docker -DockerArgs $runArgs | Out-Null
    }
    $target = "$prefix-target"; $panel = "$prefix-panel"; $other = "$prefix-other"
    $targetIP = (Invoke-Docker -DockerArgs @('inspect','--format','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',$target)).Trim()
    $panelIP = (Invoke-Docker -DockerArgs @('inspect','--format','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',$panel)).Trim()
    $otherIP = (Invoke-Docker -DockerArgs @('inspect','--format','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',$other)).Trim()
    $nat = "table ip node_test_nat {`n chain prerouting { type nat hook prerouting priority dstnat; policy accept; tcp dport 9999 dnat to ${otherIP}:9000; }`n chain postrouting { type nat hook postrouting priority srcnat; policy accept; masquerade; }`n}`n"
    [System.IO.File]::WriteAllText((Join-Path $fixture 'nat.nft'),$nat,[System.Text.UTF8Encoding]::new($false))
    Invoke-Docker -DockerArgs @('exec',$target,'nft','-f','/fixture/nat.nft') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$panel,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'9999','open') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$other,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'2222','open') | Out-Null
    Invoke-Docker -DockerArgs @('exec','-e','RM_NODE_AGENT_FIREWALL_FIXTURE=/fixture/firewall.nft','-e',"RM_FIREWALL_PANEL_IP=$panelIP",$target,'/source/.tools/node-provision.test','-test.run=TestWriteNodeAgentFirewall','-test.v=true')
    Invoke-Docker -DockerArgs @('exec',$target,'nft','-f','/fixture/firewall.nft') | Out-Null
    foreach ($peer in @($panel,$other)) {
        Invoke-Docker -DockerArgs @('exec',$peer,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'2200','open') | Out-Null
        Invoke-Docker -DockerArgs @('exec',$peer,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'80','open') | Out-Null
        Invoke-Docker -DockerArgs @('exec',$peer,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'9000','blocked') | Out-Null
        Invoke-Docker -DockerArgs @('exec',$peer,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'9999','blocked') | Out-Null
    }
    Invoke-Docker -DockerArgs @('exec',$panel,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'2222','open') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$other,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'2222','blocked') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$target,'nft','delete','table','inet','remnamanager_node') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$other,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'2222','open') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$panel,'python3','/source/tests/node-firewall-fixture.py','probe',$targetIP,'9999','open') | Out-Null
    Invoke-Docker -DockerArgs @('exec',$target,'nft','list','table','ip','node_test_nat') | Out-Null
    Write-Output 'PASS: node API restricted to panel IP; SSH/HTTP preserved; other INPUT and DNAT blocked; rollback preserves foreign NAT.'
} finally {
    foreach ($role in @('target','panel','other')) {
        $cidFile = Join-Path $fixture "$role.cid"
        if (Test-Path -LiteralPath $cidFile) {
            $cid = (Get-Content -LiteralPath $cidFile -Raw).Trim()
            if ($cid -match '^[a-f0-9]{64}$') { & docker rm -f $cid | Out-Null }
        }
    }
    if ($networkID -match '^[a-f0-9]{64}$') { & docker network rm $networkID | Out-Null }
}
