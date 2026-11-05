#!/usr/bin/env python3
"""
自动从百度网盘 Linux 官方客户端的 Cookies 数据库中提取 BDUSS
"""
import os
import sqlite3
import sys

def get_bduss():
    cookie_db = os.path.expanduser('~/.config/baidunetdisk/Cookies')
    if not os.path.exists(cookie_db):
        print(f"错误: 找不到官方客户端 Cookie 数据库: {cookie_db}", file=sys.stderr)
        print("请确认已安装并登录了官方百度网盘 Linux 客户端。", file=sys.stderr)
        sys.exit(1)

    try:
        conn = sqlite3.connect(cookie_db)
        c = conn.cursor()
        c.execute("SELECT value FROM cookies WHERE name='BDUSS' LIMIT 1")
        row = c.fetchone()
        conn.close()
        if row and row[0]:
            return row[0]
        else:
            print("错误: 在 Cookie 数据库中未找到 BDUSS，请在客户端重新登录后再试。", file=sys.stderr)
            sys.exit(1)
    except Exception as e:
        print(f"读取 Cookie 数据库异常: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == '__main__':
    bduss = get_bduss()
    if len(sys.argv) > 1 and sys.argv[1] == '--raw':
        print(bduss)
    else:
        print(f"BDUSS={bduss};")
