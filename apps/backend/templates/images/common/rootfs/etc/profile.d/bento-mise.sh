# Login shells reset PATH in /etc/profile: put the app's mise shims back in
# front of the image's tools (see /usr/local/lib/bento/env.bash).
if [ -n "${MISE_DATA_DIR:-}" ]; then
  case ":${PATH}:" in
    *":${MISE_DATA_DIR}/shims:"*) ;;
    *) PATH="${MISE_DATA_DIR}/shims:${PATH}"; export PATH ;;
  esac
fi
