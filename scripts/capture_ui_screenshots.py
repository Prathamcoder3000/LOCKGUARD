import os
import time
import json
import urllib.request
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By

def populate_test_data():
    base_url = "http://localhost/api/v1"
    headers = {"Content-Type": "application/json"}
    
    # 1. Login admin
    login_req = urllib.request.Request(
        f"{base_url}/auth/login",
        data=json.dumps({"username": "admin", "password": "admin_secure_pass_phase5"}).encode('utf-8'),
        headers=headers
    )
    with urllib.request.urlopen(login_req) as resp:
        admin_data = json.loads(resp.read().decode('utf-8'))
        admin_token = admin_data['data']['token']

    # 2. Login worker 1
    w1_req = urllib.request.Request(
        f"{base_url}/auth/login",
        data=json.dumps({"username": "client_worker", "password": "worker_secure_pass_phase5"}).encode('utf-8'),
        headers=headers
    )
    with urllib.request.urlopen(w1_req) as resp:
        w1_data = json.loads(resp.read().decode('utf-8'))
        w1_token = w1_data['data']['token']

    # 3. Login worker 2
    w2_req = urllib.request.Request(
        f"{base_url}/auth/login",
        data=json.dumps({"username": "client_worker_2", "password": "worker_secure_pass_phase5"}).encode('utf-8'),
        headers=headers
    )
    with urllib.request.urlopen(w2_req) as resp:
        w2_data = json.loads(resp.read().decode('utf-8'))
        w2_token = w2_data['data']['token']

    # 4. Worker 1 acquires order-processing-service (60s TTL)
    acq1_req = urllib.request.Request(
        f"{base_url}/locks/acquire",
        data=json.dumps({
            "resource_name": "order-processing-service",
            "owner_id": "worker-1",
            "ttl_seconds": 60
        }).encode('utf-8'),
        headers={**headers, "Authorization": f"Bearer {w1_token}"}
    )
    try:
        with urllib.request.urlopen(acq1_req) as resp:
            lock1_res = json.loads(resp.read().decode('utf-8'))
            t1 = lock1_res['data']['fencing_token']
            print(f"Acquired order-processing-service: fencing_token={t1}")
            
            # Renew once to advance lease version
            renew_req = urllib.request.Request(
                f"{base_url}/locks/renew",
                data=json.dumps({
                    "resource_name": "order-processing-service",
                    "owner_id": "worker-1",
                    "fencing_token": t1,
                    "ttl_seconds": 60
                }).encode('utf-8'),
                headers={**headers, "Authorization": f"Bearer {w1_token}"}
            )
            with urllib.request.urlopen(renew_req) as r_resp:
                print("Renewed order-processing-service: lease_version=2")
                
            # Write to protected resource
            pr_req = urllib.request.Request(
                "http://localhost:9000/api/v1/write",
                data=json.dumps({
                    "resource_name": "order-processing-service",
                    "owner_id": "worker-1",
                    "fencing_token": t1,
                    "payload": "UPDATE orders SET status='BATCH_PROCESSING';"
                }).encode('utf-8'),
                headers=headers
            )
            with urllib.request.urlopen(pr_req) as pr_resp:
                print("Protected resource write verified for order-processing-service")
    except Exception as e:
        print(f"Lock 1 notice: {e}")

    # 5. Worker 2 acquires financial-settlement-ledger (60s TTL)
    acq2_req = urllib.request.Request(
        f"{base_url}/locks/acquire",
        data=json.dumps({
            "resource_name": "financial-settlement-ledger",
            "owner_id": "worker-2",
            "ttl_seconds": 60
        }).encode('utf-8'),
        headers={**headers, "Authorization": f"Bearer {w2_token}"}
    )
    try:
        with urllib.request.urlopen(acq2_req) as resp:
            lock2_res = json.loads(resp.read().decode('utf-8'))
            t2 = lock2_res['data']['fencing_token']
            print(f"Acquired financial-settlement-ledger: fencing_token={t2}")
    except Exception as e:
        print(f"Lock 2 notice: {e}")

    return admin_data, w1_data

def capture_ui():
    admin_auth, w1_auth = populate_test_data()
    out_dir = os.path.abspath('cookbook-evidence')
    os.makedirs(out_dir, exist_ok=True)

    options = Options()
    options.add_argument('--headless')
    options.add_argument('--disable-gpu')
    options.add_argument('--window-size=1440,900')
    driver = webdriver.Chrome(options=options)

    try:
        # 1. Unauthenticated Login Page
        driver.get('http://localhost/login')
        time.sleep(2)
        driver.save_screenshot(os.path.join(out_dir, 'fig_01_login_page.png'))
        print("Captured: fig_01_login_page.png")

        # Function to inject auth state into localStorage
        def set_auth(auth_obj):
            token = auth_obj['data']['token']
            user_data = {
                "id": auth_obj['data']['user']['id'],
                "username": auth_obj['data']['user']['username'],
                "role": auth_obj['data']['user']['role'],
                "owner_id": auth_obj['data']['user'].get('owner_id', ''),
                "email": f"{auth_obj['data']['user']['username']}@lockguard.internal"
            }
            driver.execute_script(f"""
                localStorage.setItem('lockguard_token', '{token}');
                localStorage.setItem('lockguard_user', JSON.stringify({json.dumps(user_data)}));
            """)

        # 2. Login as ADMIN
        set_auth(admin_auth)

        # Overview Page
        driver.get('http://localhost/')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_02_admin_overview.png'))
        print("Captured: fig_02_admin_overview.png")

        # Cluster Consensus Page
        driver.get('http://localhost/cluster')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_03_cluster_consensus.png'))
        print("Captured: fig_03_cluster_consensus.png")

        # Locks Management Page
        driver.get('http://localhost/locks')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_04_locks_management.png'))
        print("Captured: fig_04_locks_management.png")

        # Audit Page
        driver.get('http://localhost/audit')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_05_audit_trail.png'))
        print("Captured: fig_05_audit_trail.png")

        # Resilience Page
        driver.get('http://localhost/resilience')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_06_resilience_chaos.png'))
        print("Captured: fig_06_resilience_chaos.png")

        # Benchmarks Page before probe
        driver.get('http://localhost/benchmarks')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_07_benchmarks_metrics.png'))
        print("Captured: fig_07_benchmarks_metrics.png")

        # Run live latency probe on Benchmarks page
        try:
            probe_btn = driver.find_element(By.XPATH, "//button[contains(., 'Run Live Latency Probe')]")
            probe_btn.click()
            time.sleep(4)  # Wait for 10 iterations (approx 2.5s)
            driver.save_screenshot(os.path.join(out_dir, 'fig_08_benchmarks_probe_results.png'))
            print("Captured: fig_08_benchmarks_probe_results.png")
        except Exception as e:
            print(f"Could not click probe button: {e}")

        # Settings Page
        driver.get('http://localhost/settings')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_09_system_settings.png'))
        print("Captured: fig_09_system_settings.png")

        # 3. Switch to OPERATOR (client_worker)
        set_auth(w1_auth)

        # Operator Locks View (shows "Locked (Held by worker-2)" on competitor lock)
        driver.get('http://localhost/locks')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_10_operator_locks_view.png'))
        print("Captured: fig_10_operator_locks_view.png")

        # Operator Audit View (only worker-1 events)
        driver.get('http://localhost/audit')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_11_operator_audit_isolation.png'))
        print("Captured: fig_11_operator_audit_isolation.png")

        # Operator Settings View (shows restricted banner)
        driver.get('http://localhost/settings')
        time.sleep(2.5)
        driver.save_screenshot(os.path.join(out_dir, 'fig_12_operator_settings_restricted.png'))
        print("Captured: fig_12_operator_settings_restricted.png")

        # 4. External Services
        # Prometheus Web UI
        try:
            driver.get('http://localhost:9090')
            time.sleep(2.5)
            driver.save_screenshot(os.path.join(out_dir, 'fig_13_prometheus_dashboard.png'))
            print("Captured: fig_13_prometheus_dashboard.png")
        except Exception as e:
            print(f"Prometheus capture notice: {e}")

    finally:
        driver.quit()
        print("UI Screenshot capture routine completed!")

if __name__ == '__main__':
    capture_ui()
