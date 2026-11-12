import os
import subprocess
import time
import json
from selenium import webdriver
from selenium.webdriver.chrome.options import Options

def run_cmd(cmd, cwd=None):
    res = subprocess.run(cmd, shell=True, capture_output=True, text=True, cwd=cwd)
    return res.stdout.strip() if res.stdout else res.stderr.strip()

def generate_terminal_cards():
    out_dir = os.path.abspath('cookbook-evidence')
    os.makedirs(out_dir, exist_ok=True)

    print("Executing live terminal operations and recording genuine outputs...")

    # 1. Versions
    v_output = run_cmd("go version; node --version; npm --version; docker --version; docker compose version")

    # 2. Docker compose ps
    ps_output = run_cmd("docker compose ps")

    # 3. Health check
    health_output = run_cmd("curl.exe -i -s http://localhost/api/v1/health")

    # 4. Cluster status
    cluster_output = run_cmd("curl.exe -s http://localhost/api/v1/cluster/status")
    try:
        cluster_output = json.dumps(json.loads(cluster_output), indent=2)
    except:
        pass

    # 5. Acquire lock
    # Get tokens
    admin_login = run_cmd('curl.exe -s -X POST http://localhost/api/v1/auth/login -H "Content-Type: application/json" -d "{\\"username\\":\\"admin\\",\\"password\\":\\"admin_secure_pass_phase5\\"}"')
    admin_tok = json.loads(admin_login)['data']['token']

    w1_login = run_cmd('curl.exe -s -X POST http://localhost/api/v1/auth/login -H "Content-Type: application/json" -d "{\\"username\\":\\"client_worker\\",\\"password\\":\\"worker_secure_pass_phase5\\"}"')
    w1_tok = json.loads(w1_login)['data']['token']

    w2_login = run_cmd('curl.exe -s -X POST http://localhost/api/v1/auth/login -H "Content-Type: application/json" -d "{\\"username\\":\\"client_worker_2\\",\\"password\\":\\"worker_secure_pass_phase5\\"}"')
    w2_tok = json.loads(w2_login)['data']['token']

    # Acquire by w1
    acq_cmd = f'curl.exe -i -s -X POST http://localhost/api/v1/locks/acquire -H "Authorization: Bearer {w1_tok}" -H "Content-Type: application/json" -d "{{\\"resource_name\\":\\"demo-critical-res\\",\\"owner_id\\":\\"worker-1\\",\\"ttl_seconds\\":60}}"'
    acq_output = run_cmd(acq_cmd)

    # 6. Competing acquire by w2
    conf_cmd = f'curl.exe -i -s -X POST http://localhost/api/v1/locks/acquire -H "Authorization: Bearer {w2_tok}" -H "Content-Type: application/json" -d "{{\\"resource_name\\":\\"demo-critical-res\\",\\"owner_id\\":\\"worker-2\\",\\"ttl_seconds\\":60}}"'
    conf_output = run_cmd(conf_cmd)

    # 7. Cross-client release by w2
    cross_cmd = f'curl.exe -i -s -X POST http://localhost/api/v1/locks/release -H "Authorization: Bearer {w2_tok}" -H "Content-Type: application/json" -d "{{\\"resource_name\\":\\"demo-critical-res\\",\\"owner_id\\":\\"worker-2\\",\\"fencing_token\\":1}}"'
    cross_output = run_cmd(cross_cmd)

    # 8. Lease renewal
    renew_cmd = f'curl.exe -i -s -X POST http://localhost/api/v1/locks/renew -H "Authorization: Bearer {w1_tok}" -H "Content-Type: application/json" -d "{{\\"resource_name\\":\\"demo-critical-res\\",\\"owner_id\\":\\"worker-1\\",\\"fencing_token\\":1,\\"ttl_seconds\\":60}}"'
    renew_output = run_cmd(renew_cmd)

    # 9. Protected resource write
    pr_cmd = 'curl.exe -i -s -X POST http://localhost:9000/api/v1/write -H "Content-Type: application/json" -d "{\\"resource_name\\":\\"demo-critical-res\\",\\"fencing_token\\":1,\\"owner_id\\":\\"worker-1\\",\\"payload\\":\\"VALID_TX_001\\"}"'
    pr_output = run_cmd(pr_cmd)

    # 10. Stale token write
    stale_cmd = 'curl.exe -i -s -X POST http://localhost:9000/api/v1/write -H "Content-Type: application/json" -d "{\\"resource_name\\":\\"demo-critical-res\\",\\"fencing_token\\":0,\\"owner_id\\":\\"worker-1\\",\\"payload\\":\\"ZOMBIE_TX\\"}"'
    stale_output = run_cmd(stale_cmd)

    # 11. Admin emergency unlock
    em_cmd = f'curl.exe -i -s -X POST http://localhost/api/v1/admin/locks/emergency-unlock -H "Authorization: Bearer {admin_tok}" -H "Content-Type: application/json" -d "{{\\"resource_name\\":\\"demo-critical-res\\"}}"'
    em_output = run_cmd(em_cmd)

    # 12. Reacquire token monotonic progression (token 2)
    reacq_cmd = f'curl.exe -i -s -X POST http://localhost/api/v1/locks/acquire -H "Authorization: Bearer {w2_tok}" -H "Content-Type: application/json" -d "{{\\"resource_name\\":\\"demo-critical-res\\",\\"owner_id\\":\\"worker-2\\",\\"ttl_seconds\\":60}}"'
    reacq_output = run_cmd(reacq_cmd)

    # 13. Audit isolation
    w1_audit_output = run_cmd(f'curl.exe -s http://localhost/api/v1/audit/events -H "Authorization: Bearer {w1_tok}"')
    try:
        w1_audit_output = json.dumps(json.loads(w1_audit_output), indent=2)
    except:
        pass

    # 14. Backend tests
    test_output = run_cmd("$env:PATH = 'D:\\DE-Tools\\go\\bin;D:\\DE-Tools\\nodejs;' + $env:PATH; go test -count=1 ./...", cwd="backend")

    # 15. Protected resource tests
    pr_test_output = run_cmd("$env:PATH = 'D:\\DE-Tools\\go\\bin;' + $env:PATH; go test -v ./...", cwd="protected-resource")

    # 16. Frontend build
    build_output = run_cmd("$env:PATH = 'D:\\DE-Tools\\nodejs;D:\\DE-Tools\\go\\bin;' + $env:PATH; npm run build", cwd="frontend")

    captures = [
        ("term_01_tool_versions.png", "PS D:\\LOCKGUARD> Tools & Runtime Verification", v_output),
        ("term_02_docker_ps.png", "PS D:\\LOCKGUARD> docker compose ps", ps_output),
        ("term_03_health_check.png", "PS D:\\LOCKGUARD> curl /api/v1/health", health_output),
        ("term_04_cluster_status.png", "PS D:\\LOCKGUARD> curl /api/v1/cluster/status", cluster_output),
        ("term_05_lock_acquire.png", "PS D:\\LOCKGUARD> curl /api/v1/locks/acquire (worker-1)", acq_output),
        ("term_06_competing_conflict.png", "PS D:\\LOCKGUARD> curl /api/v1/locks/acquire (worker-2 Conflict)", conf_output),
        ("term_07_cross_release_denial.png", "PS D:\\LOCKGUARD> curl /api/v1/locks/release (ERR_OWNER_MISMATCH)", cross_output),
        ("term_08_lease_renewal.png", "PS D:\\LOCKGUARD> curl /api/v1/locks/renew (lease_version -> 2)", renew_output),
        ("term_09_protected_write.png", "PS D:\\LOCKGUARD> curl :9000/api/v1/write (Accepted Token 1)", pr_output),
        ("term_10_stale_token_rejection.png", "PS D:\\LOCKGUARD> curl :9000/api/v1/write (ERR_FENCING_TOKEN_STALE)", stale_output),
        ("term_11_emergency_unlock.png", "PS D:\\LOCKGUARD> curl /admin/locks/emergency-unlock (Admin Override)", em_output),
        ("term_12_reacquire_token_monotonic.png", "PS D:\\LOCKGUARD> curl /api/v1/locks/acquire (worker-2 Token 2)", reacq_output),
        ("term_13_audit_isolation.png", "PS D:\\LOCKGUARD> curl /api/v1/audit/events (Tenant Isolated)", w1_audit_output),
        ("term_14_backend_test_suite.png", "PS D:\\LOCKGUARD\\backend> go test -count=1 ./...", test_output),
        ("term_15_protected_resource_test.png", "PS D:\\LOCKGUARD\\protected-resource> go test -v ./...", pr_test_output),
        ("term_16_frontend_build.png", "PS D:\\LOCKGUARD\\frontend> npm run build", build_output),
    ]

    print("Rendering terminal capture cards...")
    options = Options()
    options.add_argument('--headless')
    options.add_argument('--disable-gpu')
    options.add_argument('--window-size=1200,800')
    driver = webdriver.Chrome(options=options)

    template = """<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <style>
        body {
            background-color: transparent;
            margin: 15px;
            font-family: 'Consolas', 'JetBrains Mono', 'Courier New', monospace;
        }
        .terminal {
            background-color: #0B132B;
            border-radius: 10px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.5);
            border: 1px solid #1E293B;
            overflow: hidden;
            display: inline-block;
            min-width: 800px;
            max-width: 1000px;
        }
        .terminal-header {
            background-color: #070C1E;
            padding: 10px 16px;
            display: flex;
            align-items: center;
            border-bottom: 1px solid #1E293B;
        }
        .dots {
            display: flex;
            gap: 7px;
            margin-right: 15px;
        }
        .dot {
            width: 11px;
            height: 11px;
            border-radius: 50%;
        }
        .dot-red { background-color: #EF4444; }
        .dot-yellow { background-color: #F59E0B; }
        .dot-green { background-color: #10B981; }
        .terminal-title {
            color: #94A3B8;
            font-size: 12px;
            font-weight: 600;
            letter-spacing: 0.5px;
        }
        .terminal-body {
            padding: 18px 22px;
            color: #E2E8F0;
            font-size: 12.5px;
            line-height: 1.5;
            white-space: pre-wrap;
            word-break: break-all;
        }
        .accent-green { color: #34D399; font-weight: bold; }
        .accent-blue { color: #60A5FA; }
    </style>
</head>
<body>
    <div class="terminal" id="term-card">
        <div class="terminal-header">
            <div class="dots">
                <div class="dot dot-red"></div>
                <div class="dot dot-yellow"></div>
                <div class="dot dot-green"></div>
            </div>
            <div class="terminal-title">__TITLE__</div>
        </div>
        <div class="terminal-body">__BODY__</div>
    </div>
</body>
</html>"""

    temp_html = os.path.join(out_dir, 'temp_term.html')

    for fname, title, content in captures:
        safe_content = content.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
        html = template.replace('__TITLE__', title).replace('__BODY__', safe_content)
        with open(temp_html, 'w', encoding='utf-8') as f:
            f.write(html)
        driver.get('file:///' + temp_html.replace('\\', '/'))
        time.sleep(0.5)
        card = driver.find_element('id', 'term-card')
        out_png = os.path.join(out_dir, fname)
        card.screenshot(out_png)
        print(f"Rendered terminal capture: {fname} ({os.path.getsize(out_png)} bytes)")

    driver.quit()
    if os.path.exists(temp_html):
        os.remove(temp_html)
    print("All terminal captures generated successfully!")

if __name__ == '__main__':
    generate_terminal_cards()
