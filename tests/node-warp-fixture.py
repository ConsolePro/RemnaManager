"""Exercise the embedded WARP agent with isolated files and mocked system tools.

No account registration, network access or host routing changes take place.
Run with Python 3, bash and jq on Linux (or in a disposable container).
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
job = "a" * 32


def case(scenario):
    with tempfile.TemporaryDirectory(prefix="remnamanager-warp-") as temporary:
        root = Path(temporary)
        stack = root / "opt/remnanode"
        state = stack / "state"
        units = root / "etc/systemd/system"
        for directory in (state, units, root / "run/systemd/system", root / "bin"):
            directory.mkdir(parents=True, exist_ok=True)
        (state / "job-id").write_text(job)
        (state / "ready").touch()
        original = {"services": {"remnanode": {"image": "remnawave/node@sha256:" + "a" * 64, "network_mode": "host", "cap_add": ["SYS_PTRACE"], "environment": {"SECRET_KEY": "fixture-private-node-key"}}}}
        (stack / "compose.json").write_text(json.dumps(original))
        (root / "generation").write_text("1")
        (root / "rules.json").write_text(json.dumps([{"priority":32766,"src":"all","table":"main"}]))
        key = base64.b64encode(bytes([1] * 32)).decode()
        peer = base64.b64encode(bytes([2] * 32)).decode()
        seed = root / "wgcf-seed"
        seed.write_text("#!/bin/bash\nset -eu\n" +
                        'printf "wgcf %s\\n" "$*" >>"$FIXTURE/calls"\n' +
                        'case $1 in register) printf "private-account\\n" >wgcf-account.toml;;\n' +
                        "generate) cat >wgcf-profile.conf <<'CONF'\n[Interface]\nPrivateKey = " + key +
                        "\nAddress = 172.16.0.2/32, 2606:4700::2/128\n[Peer]\nPublicKey = " + peer +
                        "\nEndpoint = engage.cloudflareclient.com:2408\nAllowedIPs = 0.0.0.0/0, ::/0\nCONF\n;; esac\n")
        digest = hashlib.sha256(seed.read_bytes()).hexdigest()
        mock = root / "bin/mock"
        mock.write_text("#!/usr/bin/env python3\n" + r'''
import json,os,pathlib,re,shutil,sys
r=pathlib.Path(os.environ['FIXTURE']); name=pathlib.Path(sys.argv[0]).name; a=sys.argv[1:]
with (r/'calls').open('a') as log: log.write(name+' '+' '.join(a)+'\n')
if name=='id': print('0')
elif name in ('flock','apt-get','sleep'): pass
elif name=='uname': print('x86_64')
elif name=='ip':
    rules=json.loads((r/'rules.json').read_text())
    if a[:2]==['link','show']: sys.exit(0 if (r/'interface').exists() else 1)
    elif a==['-j','-4','rule','show']: print(json.dumps(rules))
    elif a==['-4','rule','show']:
        for rule in rules: print(str(rule['priority'])+': from '+rule.get('src','all')+' lookup '+str(rule['table']))
    elif a[:3]==['-4','rule','del']:
        priority=int(a[a.index('priority')+1]); table=int(a[a.index('lookup')+1])
        matches=[rule for rule in rules if rule['priority']==priority and rule['table']==table and
                 (('fwmark' in a and rule.get('fwmark')=='0xcab0') or ('from' in a and rule.get('src')==a[a.index('from')+1].split('/')[0]))]
        if not matches: sys.exit(2)
        rules.remove(matches[0]); (r/'rules.json').write_text(json.dumps(rules))
    elif a[:3]==['-4','route','get']:
        source='from' in a
        valid=any(rule['priority']<32766 and rule['table']==51888 and
                  (rule.get('src')=='172.16.0.2' if source else rule.get('fwmark')=='0xcab0') for rule in rules)
        device='rmwarp' if valid and os.environ['SCENARIO']!='route-broken' else 'eth0'
        print('1.1.1.1 dev '+device+' table 51888 src 172.16.0.2')
elif name=='wg':
    if a[0]=='set': (r/'endpoint').write_text(a[-1])
    elif a[-1]=='latest-handshakes': print('peer '+('0' if os.environ['SCENARIO']=='no-handshake' else '1700000000'))
    elif a[-1]=='transfer': print('peer 0 29748')
elif name=='nft':
    if a[:2]==['list','table']: sys.exit(0 if (r/'nft-active').exists() else 1)
    if a[:2]==['delete','table']: (r/'nft-active').unlink(missing_ok=True)
    if a[:1]==['-f']: (r/'nft-active').touch()
elif name=='systemctl':
    profile=r/'opt/remnanode/warp/rmwarp.conf'
    rules=json.loads((r/'rules.json').read_text())
    if 'stop' in a and profile.exists():
        priorities=[int(p) for p in re.findall(r'PostDown = ip -4 rule del priority (\d+)',profile.read_text())]
        rules=[rule for rule in rules if rule['priority'] not in priorities]
        (r/'rules.json').write_text(json.dumps(rules)); (r/'interface').unlink(missing_ok=True)
    if 'restart' in a:
        (r/'interface').touch()
        for priority,selector in re.findall(r'PostUp = ip -4 rule add priority (\d+) (.*)',profile.read_text()):
            rule={'priority':int(priority),'table':51888,'src':'all'}
            if selector.startswith('fwmark'): rule['fwmark']='0xcab0'
            else: rule['src']=selector.split()[1].split('/')[0]
            rules.append(rule)
        (r/'rules.json').write_text(json.dumps(rules))
    if a[:2]==['disable','--now'] and a[-1]=='remnamanager-node-warp.service': (r/'interface').unlink(missing_ok=True)
elif name=='curl':
    if '-o' in a: shutil.copyfile(r/'wgcf-seed',a[a.index('-o')+1])
    else:
        assert a[a.index('--interface')+1]=='172.16.0.2'
        assert '--ipv4' in a and a[a.index('--noproxy')+1]=='*'
        failed=os.environ['SCENARIO'] in ('trace-fails','no-handshake')
        if os.environ['SCENARIO']=='fallback': failed=(r/'endpoint').read_text()!='162.159.192.1:500'
        print('warp='+('off' if failed else 'on'))
elif name=='docker':
    if a[0]=='compose':
        if 'ps' in a: print('fixture-node')
        if 'config' in a: json.loads(pathlib.Path(a[a.index('-f')+1]).read_text())
        if 'up' in a:
            p=r/'generation'; p.write_text(str(int(p.read_text())+1))
    elif a[0]=='exec':
        if '-i' in a:
            c=json.load(sys.stdin)
            assert any(o.get('tag')=='rm-native-warp' for o in c['outbounds'])
            if os.environ.get('BAD_CONFIG'): sys.exit(1)
        else: print('42:'+(r/'generation').read_text())
else: raise RuntimeError(name)
''')
        mock.chmod(0o700)
        for name in ("id", "flock", "apt-get", "sleep", "uname", "ip", "wg", "nft", "systemctl", "curl", "docker"):
            (root / "bin" / name).symlink_to(mock)
        env = dict(os.environ, FIXTURE=str(root), SCENARIO=scenario,
                   PATH=str(root / "bin") + os.pathsep + os.environ["PATH"])
        agent = (repo / "internal/installer/node_warp_agent.sh").read_text()
        for path in ("/opt/remnanode", "/etc/systemd/system", "/run/systemd/system"):
            agent = agent.replace(path, str(root / path.lstrip("/")))
        assets = {"amd64": {"url": "https://github.com/ViRb3/wgcf/releases/download/v2.3.1/wgcf_2.3.1_linux_amd64", "sha256": digest}}

        def run(action, config=b"", check=True, extra=None):
            values = {"RM_JOB_ID": job, "RM_WARP_ACTION": action,
                      "RM_WARP_ASSETS": base64.b64encode(json.dumps(assets).encode()).decode(),
                      "RM_WARP_CONFIG": base64.b64encode(config).decode()}
            script = "\n".join(k + "=" + shlex.quote(v) for k, v in values.items()) + "\n" + agent
            result = subprocess.run(["bash"], input=script, text=True, capture_output=True,
                                    env=dict(env, **(extra or {})), timeout=30)
            assert key not in result.stdout + result.stderr and "private-account" not in result.stdout + result.stderr
            if check:
                assert result.returncode == 0, (action, result.stdout, result.stderr, (stack / "warp/install.log").read_text())
                assert "RM_WARP_OK=" + action in result.stdout
            return result

        if scenario == "foreign":
            target = units / "remnamanager-node-warp.service"
            target.write_text("foreign unit")
            result = run("install", check=False)
            assert result.returncode != 0 and target.read_text() == "foreign unit"
            assert not (stack / "warp/owner").exists()
        elif scenario in ("trace-fails","route-broken","no-handshake"):
            result = run("install", check=False)
            code={"trace-fails":"trace","route-broken":"route","no-handshake":"handshake"}[scenario]
            assert result.returncode != 0 and "RM_WARP_FAILURE="+code in result.stderr, result.stderr
            assert json.loads((stack / "compose.json").read_text()) == original
        else:
            if scenario=='migration':
                warp=stack/'warp'; warp.mkdir(); (warp/'owner').write_text(job)
                (warp/'rmwarp.conf').write_text('PostDown = ip -4 rule del priority 51888 fwmark 51888 lookup 51888\nPostDown = ip -4 rule del priority 51889 from 172.16.0.2/32 lookup 51888\n')
                (units/'remnamanager-node-warp.service').write_text('owned old service')
                (root/'rules.json').write_text(json.dumps([{'priority':32766,'src':'all','table':'main'},{'priority':51888,'fwmark':'0xcab0','src':'all','table':51888},{'priority':51889,'src':'172.16.0.2','table':51888}]))
            run("install")
            run("install")
            rules=json.loads((root/'rules.json').read_text())
            assert {rule['priority'] for rule in rules}=={32766,10000,10001}, rules
            if scenario=='fallback': assert (stack/'warp/endpoint').read_text().strip()=='162.159.192.1:500'
            calls = (root / "calls").read_text()
            assert calls.count("wgcf register --accept-tos") == 1
            profile = (stack / "warp/rmwarp.conf").read_text()
            assert "Table = off" in profile and "fwmark 51888" in profile and "from 172.16.0.2/32" in profile
            assert "lookup main" not in profile and "table main" not in profile
            assert (stack / "warp/rmwarp.conf").stat().st_mode & 0o777 == 0o600
            current = json.loads((stack / "compose.json").read_text())
            assert set(current["services"]["remnanode"]["cap_add"]) == {"SYS_PTRACE", "NET_ADMIN", "NET_RAW"}
            config = json.dumps({"outbounds": [{"tag": "rm-native-warp"}]}).encode()
            run("validate", config)
            failed = run("validate", config, check=False, extra={"BAD_CONFIG": "1"})
            assert failed.returncode != 0 and "RM_WARP_FAILURE=validate" in failed.stderr
            run("checkpoint")
            stale = run("validate", check=False)
            assert stale.returncode != 0, "old running process incorrectly confirmed restart"
            (root / "generation").write_text("100")
            run("validate")
            current["services"]["remnanode"]["environment"]["CUSTOM_SETTING"] = "retained"
            (stack / "compose.json").write_text(json.dumps(current))
            run("remove")
            run("remove")
            final = json.loads((stack / "compose.json").read_text())
            assert final["services"]["remnanode"]["cap_add"] == ["SYS_PTRACE"]
            assert final["services"]["remnanode"]["environment"]["CUSTOM_SETTING"] == "retained"
            assert (stack / "warp/wgcf-account.toml").exists()
            assert not (units / "remnamanager-node-warp.service").exists()
        print("WARP agent:", scenario, "passed")


for scenario in ("success", "trace-fails", "foreign", "migration", "fallback", "route-broken", "no-handshake"):
    case(scenario)
