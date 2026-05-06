mini-forwarder macOS 服务管理手册（launchd）

📌 服务信息

* 服务名：com.mini-forwarder
* 可执行文件：/usr/local/bin/mini-forwarder
* 配置文件：/etc/mini-forwarder/forwarder.yaml
* plist 路径：/Library/LaunchDaemons/com.mini-forwarder.plist
* 日志路径：
    * /var/log/mini-forwarder.log
    * /var/log/mini-forwarder.err

⸻

🚀 常用命令

1️⃣ 加载服务（首次 / 修改后）

sudo launchctl bootstrap system /Library/LaunchDaemons/com.mini-forwarder.plist

⸻

2️⃣ 启动 / 重启服务

sudo launchctl kickstart -k system/com.mini-forwarder

⸻

3️⃣ 停止服务

sudo launchctl bootout system /Library/LaunchDaemons/com.mini-forwarder.plist

⸻

4️⃣ 查看服务状态

sudo launchctl print system/com.mini-forwarder

简略查看：

sudo launchctl list | grep mini-forwarder

⸻

📊 日志查看

实时查看错误日志

tail -f /var/log/mini-forwarder.err

实时查看输出日志

tail -f /var/log/mini-forwarder.log

⸻

🔧 常见维护操作

修改配置文件后重启

sudo launchctl kickstart -k system/com.mini-forwarder

⸻

修改 plist 后重新加载

sudo launchctl bootout system /Library/LaunchDaemons/com.mini-forwarder.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.mini-forwarder.plist

⸻

确认服务是否在运行

ps aux | grep mini-forwarder

⸻

⚠️ 常见问题排查

1️⃣ 服务启动失败

优先检查：

tail -n 100 /var/log/mini-forwarder.err

⸻

2️⃣ 手动能跑，服务跑不了

检查：

* 是否使用了相对路径 ❌
* 是否依赖 shell 环境 ❌（如 .zshrc）
* 环境变量是否缺失（PATH）

⸻

3️⃣ 配置文件路径问题

macOS /etc 实际路径：

/private/etc/

如异常可改为：

/private/etc/mini-forwarder/forwarder.yaml

⸻

4️⃣ 权限问题

sudo chown root:wheel /Library/LaunchDaemons/com.mini-forwarder.plist
sudo chmod 644 /Library/LaunchDaemons/com.mini-forwarder.plist

⸻

5️⃣ 可执行权限

chmod +x /usr/local/bin/mini-forwarder

⸻

🧠 建议（长期维护）

* 日志建议接入 logrotate
* 定期检查日志文件大小
* 避免无限重启（可优化 KeepAlive）
* 重要服务建议增加监控（端口 / 进程）

⸻

📌 一句话速查

# 重启服务
sudo launchctl kickstart -k system/com.mini-forwarder
# 查看状态
sudo launchctl print system/com.mini-forwarder
# 看日志
tail -f /var/log/mini-forwarder.err

