if [[ ! -t 0 && -t 1 && -z "${BASH_SOURCE[0]:-}" && -z "${_DISK_EXPLORER_REEXEC:-}" ]]; then
  export _DISK_EXPLORER_REEXEC=1
  exec bash -c "$(cat)" bash "$@"
fi
if [[ ! -t 0 && -t 1 ]] && exec < /dev/tty 2>/dev/null; then
  echo "Reconnected to TTY"
fi
echo "Interactive: $([[ -t 0 ]] && echo Yes || echo No)"
read -t 2 -p "Try to read: " foo || echo "Read failed/timed out"
