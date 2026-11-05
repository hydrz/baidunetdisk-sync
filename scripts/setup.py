#!/usr/bin/env python3
"""
一键初始化配置脚本：
1. 自动从本机官方网盘客户端提取 BDUSS
2. 生成 .env 配置文件
3. 启动 OpenList 容器
4. 自动通过 OpenList REST API 添加“百度网盘同步空间”(/_pcs_.workspace)
5. 启动 Rclone 容器开启双向同步
"""
import os
import sys
import time
import json
import sqlite3
import subprocess
import urllib.request
import urllib.error

PROJECT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(PROJECT_DIR)

def get_bduss():
    cookie_db = os.path.expanduser('~/.config/baidunetdisk/Cookies')
    if not os.path.exists(cookie_db):
        print(f"[!] 找不到官方客户端 Cookie 数据库: {cookie_db}")
        return None
    try:
        conn = sqlite3.connect(cookie_db)
        c = conn.cursor()
        c.execute("SELECT value FROM cookies WHERE name='BDUSS' LIMIT 1")
        row = c.fetchone()
        conn.close()
        if row and row[0]:
            return row[0]
    except Exception as e:
        print(f"[!] 读取 Cookie 异常: {e}")
    return None

def init_env(bduss):
    env_file = os.path.join(PROJECT_DIR, '.env')
    example_file = os.path.join(PROJECT_DIR, '.env.example')
    
    uid = os.getuid()
    gid = os.getgid()
    
    if os.path.exists(env_file):
        print("[+] .env 文件已存在，保留现有配置。")
        with open(env_file, 'r', encoding='utf-8') as f:
            content = f.read()
    else:
        print("[+] 生成 .env 文件...")
        with open(example_file, 'r', encoding='utf-8') as f:
            content = f.read()
        content += f"\nUID={uid}\nGID={gid}\n"
    
    if bduss and 'BAIDU_BDUSS=' in content:
        lines = []
        for line in content.splitlines():
            if line.startswith('BAIDU_BDUSS='):
                lines.append(f'BAIDU_BDUSS="{bduss}"')
            else:
                lines.append(line)
        content = '\n'.join(lines) + '\n'

    with open(env_file, 'w', encoding='utf-8') as f:
        f.write(content)

    sync_dir = os.path.expanduser('~/.baidunetdisk')
    os.makedirs(sync_dir, exist_ok=True)
    print(f"[+] 本地同步目录已就绪: {sync_dir}")
    return env_file

def load_env():
    env = {}
    env_file = os.path.join(PROJECT_DIR, '.env')
    if os.path.exists(env_file):
        with open(env_file, 'r', encoding='utf-8') as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith('#') and '=' in line:
                    k, v = line.split('=', 1)
                    env[k.strip()] = v.strip().strip('"').strip("'")
    return env

def wait_for_openlist(port, timeout=60):
    url = f"http://127.0.0.1:{port}/api/public/settings"
    start = time.time()
    print(f"[+] 等待 OpenList 服务就绪 (http://127.0.0.1:{port})...")
    while time.time() - start < timeout:
        try:
            req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
            with urllib.request.urlopen(req, timeout=3) as resp:
                if resp.status == 200:
                    print("[+] OpenList 服务已在线！")
                    return True
        except Exception:
            time.sleep(2)
    return False

def setup_openlist_storage(port, admin_pwd, bduss, workspace_path):
    print("[+] 正在通过 OpenList API 登录并挂载同步空间...")
    # 1. 登录获取 Token
    login_url = f"http://127.0.0.1:{port}/api/auth/login"
    login_data = json.dumps({"username": "admin", "password": admin_pwd}).encode('utf-8')
    req = urllib.request.Request(login_url, data=login_data, headers={'Content-Type': 'application/json'})
    
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            if data.get('code') != 200:
                print(f"[!] OpenList 登录失败: {data}")
                return False
            token = data['data']['token']
    except Exception as e:
        print(f"[!] 登录请求异常: {e}")
        return False

    # 2. 检查存储是否已存在
    list_url = f"http://127.0.0.1:{port}/api/admin/storage/list"
    req = urllib.request.Request(list_url, headers={'Authorization': token})
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            storages = data.get('data', {}).get('content', [])
            for st in storages:
                if st.get('mount_path') == f"/{workspace_path}":
                    print(f"[+] 存储 /{workspace_path} 已存在，跳过创建。")
                    return True
    except Exception as e:
        print(f"[!] 检查现有存储异常: {e}")

    # 3. 创建百度网盘同步空间存储
    create_url = f"http://127.0.0.1:{port}/api/admin/storage/create"
    addition_dict = {
        "cookie": f"BDUSS={bduss};",
        "root_folder_path": "/_pcs_.workspace",
        "download_api": "official",
        "custom_download_url": "",
        "download_domain": ""
    }
    storage_payload = {
        "mount_path": f"/{workspace_path}",
        "order": 0,
        "remark": "百度网盘同步空间",
        "cache_expiration": 30,
        "web_proxy": False,
        "webdav_policy": "native_proxy",
        "down_proxy_url": "",
        "extract_folder": "",
        "driver": "BaiduNetdisk",
        "addition": json.dumps(addition_dict)
    }

    req = urllib.request.Request(
        create_url,
        data=json.dumps(storage_payload).encode('utf-8'),
        headers={'Content-Type': 'application/json', 'Authorization': token}
    )
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            if data.get('code') == 200:
                print(f"[+] 百度网盘“同步空间”(/_pcs_.workspace) 已成功挂载到 /{workspace_path}！")
                return True
            else:
                print(f"[!] 挂载存储接口返回错误: {data}")
                return False
    except Exception as e:
        print(f"[!] 挂载存储请求异常: {e}")
        return False

def main():
    print("=" * 60)
    print(" 百度网盘 Linux 双向离线同步系统 (OpenList + Rclone)")
    print("=" * 60)

    bduss = get_bduss()
    if not bduss:
        print("[!] 未能自动获取 BDUSS，请先启动并登录百度网盘官方 Linux 客户端。")
        sys.exit(1)
    print(f"[+] 成功提取到客户端 BDUSS (长度: {len(bduss)})")

    init_env(bduss)
    env = load_env()
    port = int(env.get('OPENLIST_PORT', 5244))
    admin_pwd = env.get('OPENLIST_ADMIN_PASSWORD', 'admin123')
    workspace_path = env.get('WEBDAV_WORKSPACE_PATH', 'workspace')

    # 确保宿主机持久化目录存在并赋予读写权限（OpenList v4.1.0+ 内部使用 UID 1001 运行）
    for d in ['data/openlist', 'data/rclone']:
        dir_path = os.path.join(PROJECT_DIR, d)
        os.makedirs(dir_path, exist_ok=True)
        try:
            os.chmod(dir_path, 0o777)
        except Exception:
            pass

    print("\n[+] 正在启动 OpenList 容器...")
    subprocess.run(["docker", "compose", "up", "-d", "openlist"], check=True)

    if not wait_for_openlist(port):
        print("[!] OpenList 容器启动超时，请检查 docker compose logs openlist。")
        sys.exit(1)

    # 尝试配置 storage
    setup_openlist_storage(port, admin_pwd, bduss, workspace_path)

    print("\n[+] 正在启动 Rclone 双向同步容器...")
    subprocess.run(["docker", "compose", "up", "-d", "rclone-sync"], check=True)

    print("\n" + "=" * 60)
    print("🎉 部署完成！双向同步服务已在后台运行。")
    print(f"👉 本地同步目录: {env.get('LOCAL_SYNC_DIR')}")
    print(f"👉 OpenList 面板: http://127.0.0.1:{port} (用户名: admin, 密码: {admin_pwd})")
    print(f"👉 查看同步日志: docker compose logs -f rclone-sync")
    print(f"👉 即时手动同步: ./scripts/manual-sync.sh")
    print("=" * 60)

if __name__ == '__main__':
    main()
