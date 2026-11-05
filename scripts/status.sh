#!/usr/bin/env python3
"""
运行状态一览脚本
"""
import os
import sys
import json
import subprocess
import urllib.request

PROJECT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(PROJECT_DIR)

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

def main():
    print("=" * 60)
    print(" 百度网盘 Linux 双向离线同步系统 - 状态检查")
    print("=" * 60)

    env = load_env()
    port = env.get('OPENLIST_PORT', '5244')
    admin_pwd = env.get('OPENLIST_ADMIN_PASSWORD', 'admin123')
    local_dir = os.path.expanduser(env.get('LOCAL_SYNC_DIR', '~/.baidunetdisk'))

    # 1. 容器运行状态
    print("\n📦 [1] 容器运行状态:")
    subprocess.run(["docker", "compose", "ps", "--format", "table {{.Name}}\t{{.Status}}\t{{.Ports}}"])

    # 2. 本地目录状态
    print(f"\n📂 [2] 本地同步目录 ({local_dir}):")
    if os.path.exists(local_dir):
        files_count = sum(len(files) for _, _, files in os.walk(local_dir))
        dirs_count = sum(len(dirs) for _, dirs, _ in os.walk(local_dir))
        try:
            size_out = subprocess.check_output(["du", "-sh", local_dir]).decode().split()[0]
        except Exception:
            size_out = "未知"
        print(f"   - 占用大小: {size_out}")
        print(f"   - 文件总数: {files_count} 个文件, {dirs_count} 个文件夹")
    else:
        print(f"   [!] 目录尚未创建: {local_dir}")

    # 3. OpenList 挂载点状态
    print(f"\n🌐 [3] 百度网盘“同步空间”挂载状态 (OpenList):")
    try:
        # 登录
        login_url = f"http://127.0.0.1:{port}/api/auth/login"
        login_data = json.dumps({"username": "admin", "password": admin_pwd}).encode('utf-8')
        req = urllib.request.Request(login_url, data=login_data, headers={'Content-Type': 'application/json'})
        with urllib.request.urlopen(req, timeout=3) as resp:
            token = json.loads(resp.read().decode('utf-8'))['data']['token']

        # 查存储
        list_url = f"http://127.0.0.1:{port}/api/admin/storage/list"
        req = urllib.request.Request(list_url, headers={'Authorization': token})
        with urllib.request.urlopen(req, timeout=3) as resp:
            storages = json.loads(resp.read().decode('utf-8')).get('data', {}).get('content', [])
            workspace_st = next((s for s in storages if s.get('mount_path') == f"/{env.get('WEBDAV_WORKSPACE_PATH', 'workspace')}"), None)
            if workspace_st:
                status = workspace_st.get('status', '未知')
                color_flag = "✅" if status == "work" else "⚠️"
                print(f"   - 挂载路径: {workspace_st.get('mount_path')}")
                print(f"   - 驱动类型: {workspace_st.get('driver')}")
                print(f"   - 当前状态: {color_flag} {status}")
                if status != "work":
                    print(f"   [!] 提示: 存储状态异常，请在后台 (http://127.0.0.1:{port}) 或运行 python3 scripts/setup.py 进行配置更新。")
            else:
                print("   [!] 尚未在 OpenList 中检测到 /workspace 存储。")
    except Exception as e:
        print(f"   [!] 无法连接 OpenList API: {e}")

    # 4. 同步日志
    print("\n📜 [4] 最新同步守护进程日志 (尾部 10 行):")
    subprocess.run(["docker", "compose", "logs", "--tail=10", "rclone-sync"])

    print("\n" + "=" * 60)

if __name__ == '__main__':
    main()
