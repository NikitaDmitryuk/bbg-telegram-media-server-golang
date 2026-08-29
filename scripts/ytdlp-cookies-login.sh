#!/bin/sh
set -eu

remote_host=${REMOTE_SSH:-telegram-server}
local_port=${YTDLP_LOGIN_PORT:-9222}
remote_service=${YTDLP_LOGIN_SERVICE:-telegram-media-server-youtube-login.service}
exporter=${YTDLP_COOKIE_EXPORTER:-/usr/local/libexec/telegram-media-server-youtube-cookies-export}
tunnel_pid=

cleanup() {
	if [ -n "${tunnel_pid}" ]; then
		kill "${tunnel_pid}" 2>/dev/null || true
		wait "${tunnel_pid}" 2>/dev/null || true
	fi
	ssh "${remote_host}" "sudo systemctl stop ${remote_service}; sudo ${exporter} --cleanup" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

ssh "${remote_host}" "sudo systemctl start ${remote_service}"
ssh -N -L "${local_port}:127.0.0.1:9222" "${remote_host}" &
tunnel_pid=$!

attempt=0
until curl --silent --fail --max-time 2 "http://127.0.0.1:${local_port}/json/list" >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	if [ "${attempt}" -ge 30 ]; then
		echo "Chromium remote debugging did not become ready." >&2
		exit 1
	fi
	sleep 1
done

login_url="http://127.0.0.1:${local_port}"
echo "Open ${login_url}, click inspect, enable the DevTools screencast, and sign in to the dedicated Google account."
echo "After login, navigate the remote tab to https://www.youtube.com/robots.txt."
if command -v open >/dev/null 2>&1; then
	open "${login_url}"
elif command -v xdg-open >/dev/null 2>&1; then
	xdg-open "${login_url}"
fi
printf "Press Enter only after the remote tab shows youtube.com/robots.txt: "
read -r _answer

ssh "${remote_host}" "sudo systemctl stop ${remote_service}; sudo ${exporter}"
echo "YouTube cookies were validated and installed. No TMS restart is required."
