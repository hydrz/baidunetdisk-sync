#!/usr/bin/env python3
"""
一键初始化配置流水线：
1. 自动从本机官方网盘客户端提取 BDUSS（备查）
2. 引导配置百度网盘 Refresh Token
3. 设置宿主机目录权限与 .env 环境变量
4. 启动 OpenList 容器并通过 API 全自动挂载同步空间 (/_pcs_.workspace)
5. 启动 Rclone 双向同步引擎
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
        return None
    try:
        conn = sqlite3.connect(cookie_db)
        c = conn.cursor()
        c.execute("SELECT value FROM cookies WHERE name='BDUSS' LIMIT 1")
        row = c.fetchone()
        conn.close()
        if row and row[0]:
            return row[0]
    except Exception:
        pass
    return None

def init_env(bduss, refresh_token=None):
    env_file = os.path.join(PROJECT_DIR, '.env')
    example_file = os.path.join(PROJECT_DIR, '.env.example')
    
    uid = os.getuid()
    gid = os.getgid()
    
    if os.path.exists(env_file):
        with open(env_file, 'r', encoding='utf-8') as f:
            content = f.read()
    else:
        with open(example_file, 'r', encoding='utf-8') as f:
            content = f.read()
        content += f"\nUID={uid}\nGID={gid}\n"
    
    lines = content.splitlines()
    has_bduss = any(l.startswith('BAIDU_BDUSS=') for l in lines)
    has_token = any(l.startswith('BAIDU_REFRESH_TOKEN=') for l in lines)
    
    new_lines = []
    for l in lines:
        if l.startswith('BAIDU_BDUSS=') and bduss:
            new_lines.append(f'BAIDU_BDUSS="{bduss}"')
        elif l.startswith('BAIDU_REFRESH_TOKEN=') and refresh_token:
            new_lines.append(f'BAIDU_REFRESH_TOKEN="{refresh_token}"')
        else:
            new_lines.append(l)
            
    if not has_token and refresh_token:
        new_lines.append(f'BAIDU_REFRESH_TOKEN="{refresh_token}"')
        
    content = '\n'.join(new_lines) + '\n'

    with open(env_file, 'w', encoding='utf-8') as f:
        f.write(content)

    sync_dir = os.path.expanduser('~/.baidunetdisk')
    os.makedirs(sync_dir, exist_ok=True)
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

def configure_storage(port, admin_pwd, refresh_token, workspace_path):
    print("[+] 正在连接 OpenList API 配置同步空间存储...")
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
            auth_token = data['data']['token']
    except Exception as e:
        print(f"[!] 登录请求异常: {e}")
        return False

    addition_dict = {
        "root_folder_path": "/_pcs_.workspace",
        "order_by": "name",
        "order_direction": "asc",
        "download_api": "official",
        "use_online_api": True,
        "api_url_address": "https://api.oplist.org/baiduyun/renewapi",
        "client_id": "",
        "client_secret": "",
        "custom_crack_ua": "netdisk",
        "refresh_token": refresh_token or "",
        "upload_thread": "3",
        "upload_timeout": 60,
        "upload_api": "https://d.pcs.baidu.com",
        "use_dynamic_upload_api": True,
        "custom_upload_part_size": 0,
        "low_bandwith_upload_mode": False,
        "only_list_video_file": False
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

    # 检查是否已存在存储
    list_url = f"http://127.0.0.1:{port}/api/admin/storage/list"
    req = urllib.request.Request(list_url, headers={'Authorization': auth_token})
    existing_id = None
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            storages = data.get('data', {}).get('content', [])
            for st in storages:
                if st.get('mount_path') == f"/{workspace_path}":
                    existing_id = st.get('id')
                    break
    except Exception:
        pass

    target_url = f"http://127.0.0.1:{port}/api/admin/storage/update" if existing_id else f"http://127.0.0.1:{port}/api/admin/storage/create"
    if existing_id:
        storage_payload["id"] = existing_id

    req = urllib.request.Request(
        target_url,
        data=json.dumps(storage_payload).encode('utf-8'),
        headers={'Content-Type': 'application/json', 'Authorization': auth_token}
    )
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            if data.get('code') == 200:
                print(f"[+] 百度网盘“同步空间”配置成功！")
                return True
            else:
                print(f"[!] 存储配置接口提示: {data.get('message')}")
                return False
    except Exception as e:
        print(f"[!] 存储配置请求异常: {e}")
        return False

def main():
    print("=" * 60)
    print(" 百度网盘 Linux 双向离线同步系统 (OpenList + Rclone)")
    print("=" * 60)

    # 1. 自动提取本地已登录客户端的 BDUSS
    bduss = get_bduss()
    if bduss:
        print(f"[+] 已检测到官方客户端登录状态 (BDUSS: {bduss[:15]}...)")
    
    # 2. 检查或引导配置 Refresh Token
    env = load_env()
    refresh_token = env.get('BAIDU_REFRESH_TOKEN', '')
    
    if not refresh_token:
        print("\n" + "-" * 60)
        print("👉 首次配置提示：获取百度网盘授权令牌 (Refresh Token)")
        print("1. 浏览器打开 OpenList 官方授权页面: https://api.oplist.org/")
        print("2. 点击「百度网盘」并授权登录")
        print("3. 授权成功后复制生成的 Refresh Token")
        print("-" * 60)
        try:
            val = input("请输入 Refresh Token (直接回车可稍后在网页端填写): ").strip()
            if val:
                refresh_token = val
        except (EOFError, KeyboardInterrupt):
            pass

    # 3. 初始化 .env 和宿主机目录权限
    init_env(bduss, refresh_token)
    env = load_env()
    port = int(env.get('OPENLIST_PORT', 5244))
    admin_pwd = env.get('OPENLIST_ADMIN_PASSWORD', 'admin123')
    workspace_path = env.get('WEBDAV_WORKSPACE_PATH', 'workspace')

    for d in ['data/openlist', 'data/rclone']:
        path = os.path.join(PROJECT_DIR, d)
        os.makedirs(path, exist_ok=True)
        try:
            os.chmod(path, 0o777)
        except Exception:
            pass

    # 4. 启动 OpenList 容器
    print("\n[+] 正在启动 OpenList 服务容器...")
    subprocess.run(["docker", "compose", "up", "-d", "openlist"], check=True)

    if not wait_for_openlist(port):
        print("[!] OpenList 服务启动超时，请检查 docker compose logs openlist。")
        sys.exit(1)

    # 5. 配置百度网盘同步空间存储
    configure_storage(port, admin_pwd, refresh_token, workspace_path)

    # 6. 启动 Rclone 容器
    print("\n[+] 正在启动 Rclone 双向同步容器...")
    subprocess.run(["docker", "compose", "up", "-d", "rclone-sync"], check=True)

    print("\n" + "=" * 60)
    print("🎉 部署完成！")
    print(f"👉 本地同步目录: ~/.baidunetdisk")
    print(f"👉 OpenList 面板: http://127.0.0.1:{port} (用户名: admin, 密码: {admin_pwd})")
    print(f"👉 系统状态检测: ./scripts/status.sh")
    print(f"👉 手动即时同步: ./scripts/manual-sync.sh")
    print(f"👉 查看实时日志: docker compose logs -f rclone-sync")
    print("=" * 60)

if __name__ == '__main__':
    main()
