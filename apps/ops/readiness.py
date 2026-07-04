"""Process-local readiness drain state for graceful shutdown."""

from __future__ import annotations

import signal
from collections.abc import Callable, Iterable
from types import FrameType

_draining = False
_installed = False
_previous_handlers: dict[int, Callable[[int, FrameType | None], object] | int | None] = {}


def is_draining() -> bool:
    return _draining


def mark_draining() -> None:
    """Flip readiness into drain mode without exposing host/process details."""

    global _draining
    _draining = True


def reset_draining_for_tests() -> None:
    global _draining
    _draining = False


def install_shutdown_signal_handlers(
    signals: Iterable[signal.Signals] = (signal.SIGTERM, signal.SIGINT),
) -> None:
    """Mark readiness as draining on shutdown signals, then preserve existing handlers.

    ASGI/WSGI servers may install their own graceful-shutdown handlers. This wrapper sets the
    readiness bit first and then delegates to any existing callable handler. If no handler existed,
    keep standard termination semantics instead of turning SIGTERM/SIGINT into no-ops.
    """

    global _installed
    if _installed:
        return

    for shutdown_signal in signals:
        previous_handler = signal.getsignal(shutdown_signal)
        if previous_handler is _handle_shutdown_signal:
            continue
        _previous_handlers[int(shutdown_signal)] = previous_handler
        signal.signal(shutdown_signal, _handle_shutdown_signal)
    _installed = True


def _handle_shutdown_signal(signum: int, frame: FrameType | None) -> None:
    mark_draining()

    previous_handler = _previous_handlers.get(signum)
    if callable(previous_handler):
        previous_handler(signum, frame)
        return

    if previous_handler == signal.SIG_IGN:
        return

    if signum == signal.SIGINT:
        raise KeyboardInterrupt
    raise SystemExit(128 + signum)
