#!/usr/bin/env python3
"""A small native Podman Libpod API TUI."""

from __future__ import annotations

import curses
import http.client
import json
import os
import socket
import subprocess
import sys
import time
import tomllib
from dataclasses import dataclass, field
from typing import Any
from urllib.parse import urlencode


DEFAULT_API_VERSION = "v5.0.0"
REFRESH_SECONDS = 2.0
RESOURCE_MODES = ("containers", "pods", "images", "volumes", "networks")
RESOURCE_LABELS = {
    "containers": "Containers",
    "pods": "Pods",
    "images": "Images",
    "volumes": "Volumes",
    "networks": "Networks",
}
SPARK_CHARS = "▁▂▃▄▅▆▇█"
LIFECYCLE_KEYS = {
    ord("S"): "start",
    ord("s"): "stop",
    ord("r"): "restart",
    ord("R"): "restart",
}


class PodmanError(RuntimeError):
    """An actionable Podman API or connection error."""


class UnixHTTPConnection(http.client.HTTPConnection):
    """HTTPConnection that talks to Podman's Unix domain socket."""

    def __init__(self, socket_path: str, timeout: float = 5.0) -> None:
        super().__init__("podman", timeout=timeout)
        self.socket_path = socket_path

    def connect(self) -> None:
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.socket_path)


class PodmanClient:
    """Minimal client for the Podman-native Libpod REST API."""

    def __init__(self, socket_path: str | None = None) -> None:
        runtime_dir = os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}")
        self.socket_path = socket_path or os.environ.get(
            "LZPODY_SOCKET",
            os.environ.get("PODMAN_TUI_SOCKET", os.path.join(runtime_dir, "podman/podman.sock")),
        )
        self.api_version = os.environ.get(
            "LZPODY_API_VERSION",
            os.environ.get("PODMAN_TUI_API_VERSION", DEFAULT_API_VERSION),
        )
        self.api_root = f"/{self.api_version}/libpod"

    def _request(
        self,
        method: str,
        path: str,
        query: dict[str, Any] | None = None,
        body: bytes | None = None,
    ) -> Any:
        if not path.startswith("/"):
            path = "/" + path
        if query:
            query_values: list[tuple[str, Any]] = []
            for key, value in query.items():
                values = value if isinstance(value, (list, tuple)) else (value,)
                for entry in values:
                    query_values.append((key, str(entry).lower() if isinstance(entry, bool) else entry))
            encoded = urlencode(query_values)
            path = f"{path}?{encoded}"

        connection = UnixHTTPConnection(self.socket_path)
        try:
            connection.request(
                method,
                path,
                body=body,
                headers={
                    "Accept": "application/json",
                    "Content-Type": "application/json",
                },
            )
            response = connection.getresponse()
            raw = response.read()
        except FileNotFoundError as exc:
            raise PodmanError(
                f"Podman socket not found: {self.socket_path}. "
                "Recover with: systemctl --user restart podman.socket"
            ) from exc
        except (ConnectionError, OSError, TimeoutError) as exc:
            raise PodmanError(f"Cannot connect to Podman: {exc}") from exc
        finally:
            connection.close()

        if response.status >= 300:
            message = raw.decode("utf-8", errors="replace").strip()
            if len(message) > 240:
                message = message[:237] + "..."
            raise PodmanError(f"Podman API {response.status}: {message or response.reason}")

        if not raw:
            return None
        content_type = response.getheader("Content-Type", "")
        if "json" in content_type:
            try:
                return json.loads(raw)
            except json.JSONDecodeError as exc:
                raise PodmanError("Podman returned invalid JSON") from exc
        try:
            return json.loads(raw)
        except (json.JSONDecodeError, UnicodeDecodeError):
            return raw

    def containers(self) -> list[dict[str, Any]]:
        result = self._request("GET", f"{self.api_root}/containers/json", {"all": True})
        return result if isinstance(result, list) else []

    def pods(self) -> list[dict[str, Any]]:
        result = self._request("GET", f"{self.api_root}/pods/json")
        return result if isinstance(result, list) else []

    def images(self) -> list[dict[str, Any]]:
        result = self._request("GET", f"{self.api_root}/images/json")
        return result if isinstance(result, list) else []

    def volumes(self) -> list[dict[str, Any]]:
        result = self._request("GET", f"{self.api_root}/volumes/json")
        if isinstance(result, dict):
            result = result.get("Volumes") or []
        return result if isinstance(result, list) else []

    def networks(self) -> list[dict[str, Any]]:
        result = self._request("GET", f"{self.api_root}/networks/json")
        return result if isinstance(result, list) else []

    def inspect_container(self, container_id: str) -> Any:
        return self._request("GET", f"{self.api_root}/containers/{container_id}/json")

    def inspect_pod(self, pod_id: str) -> Any:
        return self._request("GET", f"{self.api_root}/pods/{pod_id}/json")

    def inspect_image(self, image_id: str) -> Any:
        return self._request("GET", f"{self.api_root}/images/{image_id}/json")

    def inspect_volume(self, volume_name: str) -> Any:
        return self._request("GET", f"{self.api_root}/volumes/{volume_name}/json")

    def inspect_network(self, network_name: str) -> Any:
        return self._request("GET", f"{self.api_root}/networks/{network_name}/json")

    def logs(self, container_id: str) -> str:
        raw = self._request(
            "GET",
            f"{self.api_root}/containers/{container_id}/logs",
            {"stdout": True, "stderr": True, "timestamps": True, "tail": 200},
        )
        if isinstance(raw, bytes):
            return raw.decode("utf-8", errors="replace")
        if isinstance(raw, str):
            return raw
        return json.dumps(raw, indent=2, ensure_ascii=False)

    def stats(self, container_id: str) -> Any:
        return self._request(
            "GET",
            f"{self.api_root}/containers/stats",
            {"containers": [container_id], "stream": False},
        )

    def top(self, container_id: str) -> Any:
        return self._request("GET", f"{self.api_root}/containers/{container_id}/top")

    def pod_stats(self, pod_id: str) -> Any:
        return self._request(
            "GET",
            f"{self.api_root}/pods/stats",
            {"namesOrIDs": [pod_id], "all": True, "stream": False},
        )

    def container_action(self, container_id: str, action: str) -> None:
        if action == "remove":
            self._request("DELETE", f"{self.api_root}/containers/{container_id}", {"force": True})
            return
        if action not in {"start", "stop", "restart", "pause", "unpause", "kill"}:
            raise ValueError(f"Unsupported container action: {action}")
        if action == "kill":
            query = {"signal": "SIGKILL"}
        else:
            query = {"timeout": 10} if action in {"stop", "restart"} else None
        self._request("POST", f"{self.api_root}/containers/{container_id}/{action}", query)

    def pod_action(self, pod_id: str, action: str) -> None:
        if action == "remove":
            self._request("DELETE", f"{self.api_root}/pods/{pod_id}", {"force": True})
            return
        if action not in {"start", "stop", "restart", "pause", "unpause", "kill"}:
            raise ValueError(f"Unsupported pod action: {action}")
        if action == "kill":
            query = {"signal": "SIGKILL"}
        else:
            query = {"timeout": 10} if action in {"stop", "restart"} else None
        self._request("POST", f"{self.api_root}/pods/{pod_id}/{action}", query)

    def remove_image(self, image_id: str) -> None:
        self._request("DELETE", f"{self.api_root}/images/{image_id}", {"force": True})

    def remove_volume(self, volume_name: str) -> None:
        self._request("DELETE", f"{self.api_root}/volumes/{volume_name}", {"force": True})

    def remove_network(self, network_name: str) -> None:
        self._request("DELETE", f"{self.api_root}/networks/{network_name}", {"force": True})


@dataclass
class Item:
    kind: str
    item_id: str
    name: str
    state: str
    image: str = ""
    status: str = ""
    health: str = ""
    cpu: float = 0.0
    ports: list[str] = field(default_factory=list)
    details: dict[str, Any] = field(default_factory=dict)


def first_name(value: Any) -> str:
    if isinstance(value, list):
        return str(value[0]) if value else ""
    return str(value or "")


def port_mappings(raw: dict[str, Any]) -> list[str]:
    values = raw.get("Ports") or raw.get("PortMappings") or []
    if isinstance(values, dict):
        values = [values]
    mappings: list[str] = []
    for value in values:
        if not isinstance(value, dict):
            text = str(value).strip()
            if text:
                mappings.append(text)
            continue
        container_port = value.get("container_port") or value.get("containerPort") or value.get("container_port_num")
        host_port = value.get("host_port") or value.get("hostPort")
        protocol = str(value.get("protocol") or "tcp")
        host_ip = str(value.get("host_ip") or value.get("hostIP") or "0.0.0.0")
        if host_port:
            mappings.append(f"{host_ip}:{host_port}->{container_port}/{protocol}")
        elif container_port:
            mappings.append(f"{container_port}/{protocol}")
    return mappings


def container_item(raw: dict[str, Any]) -> Item:
    item_id = str(raw.get("Id") or raw.get("ID") or "")
    name = first_name(raw.get("Names") or raw.get("Name"))
    return Item(
        kind="container",
        item_id=item_id,
        name=name.lstrip("/") or item_id[:12],
        state=str(raw.get("State") or "unknown"),
        image=str(raw.get("Image") or ""),
        status=str(raw.get("Status") or ""),
        health=health_status(raw),
        ports=port_mappings(raw),
    )


def health_status(raw: Any, default: str = "unknown") -> str:
    if not isinstance(raw, dict):
        return default

    candidates: list[Any] = [raw.get("Health"), raw.get("Healthcheck")]
    state = raw.get("State")
    if isinstance(state, dict):
        candidates.extend([state.get("Health"), state.get("Healthcheck"), state.get("HealthStatus")])
    for candidate in candidates:
        if isinstance(candidate, str) and candidate.strip():
            return candidate.strip().lower()
        if isinstance(candidate, dict):
            status = candidate.get("Status") or candidate.get("status")
            if status:
                return str(status).strip().lower()

    config = raw.get("Config")
    if isinstance(config, dict) and "Healthcheck" in config:
        return "no healthcheck" if config["Healthcheck"] is None else "not started"
    return default


def container_state_label(item: Item) -> str:
    if item.state.lower() == "exited" and item.status.startswith("Exited ("):
        end = item.status.find(")")
        if end >= 0:
            return item.status[: end + 1].lower()
    return item.state


def pod_item(raw: dict[str, Any]) -> Item:
    item_id = str(raw.get("Id") or raw.get("ID") or "")
    return Item(
        kind="pod",
        item_id=item_id,
        name=str(raw.get("Name") or item_id[:12]),
        state=str(raw.get("Status") or "unknown"),
        status=f"{len(raw.get('Containers') or [])} containers",
    )


def size_text(value: Any) -> str:
    try:
        size = float(value or 0)
    except (TypeError, ValueError):
        return ""
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if abs(size) < 1024 or unit == "TiB":
            return f"{size:.1f} {unit}"
        size /= 1024
    return ""


def number_value(value: Any) -> float:
    if isinstance(value, str):
        value = value.strip().rstrip("%")
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def bytes_text(value: Any) -> str:
    return size_text(number_value(value)) or "0 B"


def gauge(value: float, width: int = 20) -> str:
    ratio = max(0.0, min(1.0, value / 100.0))
    filled = round(ratio * width)
    return "█" * filled + "░" * (width - filled)


def sparkline(values: list[float], width: int = 32) -> str:
    if not values:
        return "(no samples)"
    values = values[-width:]
    low = min(values)
    high = max(values)
    if high == low:
        index = 0 if high <= 0 else len(SPARK_CHARS) // 2
        return SPARK_CHARS[index] * len(values)
    scale = len(SPARK_CHARS) - 1
    return "".join(SPARK_CHARS[round((value - low) / (high - low) * scale)] for value in values)


def stats_payload(value: Any) -> dict[str, Any] | None:
    if isinstance(value, list):
        value = value[0] if value else None
    if not isinstance(value, dict):
        return None
    stats = value.get("Stats")
    if isinstance(stats, list):
        return stats[0] if stats and isinstance(stats[0], dict) else None
    return value


def stats_lines(history: list[dict[str, Any]]) -> list[str]:
    if not history:
        return ["No statistics available."]

    def value_for(sample: dict[str, Any], key: str, fallback: str | None = None) -> float:
        value = sample.get(key)
        if value is None and fallback:
            value = sample.get(fallback)
        return number_value(value)

    latest = history[-1]
    cpu_values = [value_for(sample, "CPU", "AvgCPU") for sample in history]
    memory_values = [value_for(sample, "MemPerc") for sample in history]
    cpu = cpu_values[-1]
    memory = memory_values[-1]
    memory_usage = latest.get("MemUsage")
    memory_limit = latest.get("MemLimit")
    pids = int(number_value(latest.get("PIDs")))
    block_input = latest.get("BlockInput")
    block_output = latest.get("BlockOutput")
    return [
        "Live container statistics",
        "",
        f"CPU       {cpu:6.2f}%  [{gauge(cpu)}]",
        f"          {sparkline(cpu_values)}",
        f"Memory    {memory:6.2f}%  [{gauge(memory)}]",
        f"          {sparkline(memory_values)}",
        f"Usage     {bytes_text(memory_usage)} / {bytes_text(memory_limit)}",
        f"PIDs      {pids}",
        f"Block I/O in   {bytes_text(block_input)}",
        f"Block I/O out  {bytes_text(block_output)}",
        "",
        f"Samples: {len(history)}  ·  refresh interval: {REFRESH_SECONDS:g}s",
    ]


def image_item(raw: dict[str, Any]) -> Item:
    item_id = str(raw.get("Id") or raw.get("ID") or "")
    name = first_name(raw.get("RepoTags")) or item_id[:12]
    return Item(
        kind="image",
        item_id=item_id,
        name=name,
        state="image",
        status=size_text(raw.get("Size")),
        details=raw,
    )


def volume_item(raw: dict[str, Any]) -> Item:
    name = str(raw.get("Name") or "")
    return Item(
        kind="volume",
        item_id=name,
        name=name,
        state="volume",
        status=str(raw.get("Driver") or "local"),
        details=raw,
    )


def network_item(raw: dict[str, Any]) -> Item:
    name = str(raw.get("Name") or raw.get("name") or "")
    return Item(
        kind="network",
        item_id=name,
        name=name,
        state="network",
        status=str(raw.get("Driver") or raw.get("driver") or "bridge"),
        details=raw,
    )


class App:
    def __init__(self, client: PodmanClient) -> None:
        self.client = client
        self.mode = "containers"
        self.items_by_mode: dict[str, list[Item]] = {mode: [] for mode in RESOURCE_MODES}
        self.selected_by_mode: dict[str, int] = {mode: 0 for mode in RESOURCE_MODES}
        self.detail_mode = "summary"
        self.detail_lines: list[str] = []
        self.stats_history: dict[str, list[dict[str, Any]]] = {}
        self.filter_text = ""
        self.hide_stopped = False
        self.status = "Connecting to rootless Podman..."
        self.last_refresh = 0.0
        self.running = True
        self.focus = "side"
        self.detail_scroll = 0
        self.detail_view_rows = 1
        self.redraw: Any = None
        self.menu_open = False
        self.menu_index = 0

    @property
    def items(self) -> list[Item]:
        return self.items_by_mode[self.mode]

    @items.setter
    def items(self, value: list[Item]) -> None:
        self.items_by_mode[self.mode] = value

    @property
    def selected(self) -> int:
        return self.selected_by_mode[self.mode]

    @selected.setter
    def selected(self, value: int) -> None:
        self.selected_by_mode[self.mode] = value

    @property
    def current(self) -> Item | None:
        return self.items[self.selected] if self.items and 0 <= self.selected < len(self.items) else None

    def refresh(self, keep_id: str | None = None) -> None:
        loaders = {
            "containers": (self.client.containers, container_item),
            "pods": (self.client.pods, pod_item),
            "images": (self.client.images, image_item),
            "volumes": (self.client.volumes, volume_item),
            "networks": (self.client.networks, network_item),
        }
        needle = self.filter_text.lower()
        error: str | None = None
        for mode in RESOURCE_MODES:
            loader, converter = loaders[mode]
            try:
                raw_items = loader()
                converted = [converter(item) for item in raw_items]
                if mode == "containers":
                    for item in converted:
                        try:
                            sample = stats_payload(self.client.stats(item.item_id))
                            if sample:
                                item.cpu = number_value(
                                    sample.get("CPU")
                                    if sample.get("CPU") is not None
                                    else sample.get("AvgCPU")
                                )
                        except PodmanError:
                            pass
                self.items_by_mode[mode] = [
                    item
                    for item in converted
                    if not (self.hide_stopped and mode == "containers" and item.state.lower() not in {"running", "paused"})
                    and (not needle or needle in f"{item.name} {item.image} {item.state}".lower())
                ]
                if mode == self.mode and keep_id:
                    self.selected_by_mode[mode] = next(
                        (index for index, item in enumerate(self.items_by_mode[mode]) if item.item_id == keep_id),
                        0,
                    )
                else:
                    self.selected_by_mode[mode] = min(
                        self.selected_by_mode[mode], max(0, len(self.items_by_mode[mode]) - 1)
                    )
            except PodmanError as exc:
                error = error or str(exc)
                self.items_by_mode[mode] = []
                self.selected_by_mode[mode] = 0

        self.status = error or f"Updated {time.strftime('%H:%M:%S')}"
        self.last_refresh = time.monotonic()
        if self.current:
            if self.detail_mode == "summary":
                self.load_summary()
            elif self.detail_mode == "logs" and self.current.kind == "container":
                self.load_logs(silent=True)
            elif self.detail_mode == "stats" and self.current.kind in {"container", "pod"}:
                self.load_stats(silent=True)
            elif self.detail_mode == "top" and self.current.kind == "container":
                self.load_top(silent=True)
        else:
            self.detail_mode = "summary"
            self.detail_lines = ["No item selected."]

    def load_summary(self) -> None:
        item = self.current
        if not item:
            self.detail_lines = ["No item selected."]
            self.detail_scroll = 0
            return
        self.detail_lines = [
            f"Name:    {item.name}",
            f"ID:      {item.item_id}",
            f"State:   {item.state}",
            f"Status:  {item.status}",
        ]
        if item.kind == "container":
            health = item.health or "unknown"
            try:
                inspected = self.client.inspect_container(item.item_id)
                health = health_status(inspected, default="no healthcheck")
                item.health = health
            except PodmanError:
                pass
            self.detail_lines.append(f"Health:  {health}")
            self.detail_lines.append(f"CPU:     {item.cpu:.2f}%")
            self.detail_lines.append("Ports / forwarding:")
            if item.ports:
                self.detail_lines.extend(f"  {port}" for port in item.ports)
            else:
                self.detail_lines.append("  (none)")
        if item.image:
            self.detail_lines.append(f"Image:   {item.image}")
        self.detail_lines.append("")
        self.detail_lines.append("Press x or ? to open available actions.")
        self.detail_scroll = 0

    def load_logs(self, silent: bool = False) -> None:
        item = self.current
        if not item or item.kind != "container":
            return
        try:
            self.detail_lines = self.client.logs(item.item_id).splitlines() or ["(no logs)"]
            self.detail_mode = "logs"
            if not silent:
                self.detail_scroll = 0
            if not silent:
                self.status = f"Logs: {item.name}"
        except PodmanError as exc:
            self.status = str(exc)

    def load_stats(self, silent: bool = False) -> None:
        item = self.current
        if not item or item.kind not in {"container", "pod"}:
            return
        try:
            value = self.client.stats(item.item_id) if item.kind == "container" else self.client.pod_stats(item.item_id)
            sample = stats_payload(value)
            if sample is None:
                self.detail_lines = ["No statistics available."]
            else:
                history = self.stats_history.setdefault(item.item_id, [])
                history.append(sample)
                del history[:-60]
                self.detail_lines = stats_lines(history)
            self.detail_mode = "stats"
            if not silent:
                self.detail_scroll = 0
            if not silent:
                self.status = f"Stats: {item.name}"
        except PodmanError as exc:
            self.status = str(exc)

    def load_inspect(self, detail_mode: str = "config") -> None:
        item = self.current
        if not item:
            return
        try:
            inspectors = {
                "container": self.client.inspect_container,
                "pod": self.client.inspect_pod,
                "image": self.client.inspect_image,
                "volume": self.client.inspect_volume,
                "network": self.client.inspect_network,
            }
            value = inspectors[item.kind](item.item_id)
            self.detail_lines = json.dumps(value, indent=2, ensure_ascii=False).splitlines()
            self.detail_mode = detail_mode
            self.detail_scroll = 0
            self.status = f"{detail_mode.title()}: {item.name}"
        except PodmanError as exc:
            self.status = str(exc)

    def load_env(self) -> None:
        item = self.current
        if not item or item.kind != "container":
            return
        try:
            value = self.client.inspect_container(item.item_id)
            environment = ((value.get("Config") or {}).get("Env") or []) if isinstance(value, dict) else []
            self.detail_lines = [str(entry) for entry in environment] or ["(no environment variables)"]
            self.detail_mode = "env"
            self.detail_scroll = 0
            self.status = f"Environment: {item.name}"
        except PodmanError as exc:
            self.status = str(exc)

    def load_top(self, silent: bool = False) -> None:
        item = self.current
        if not item or item.kind != "container":
            return
        try:
            value = self.client.top(item.item_id)
            if isinstance(value, dict):
                titles = [str(title) for title in (value.get("Titles") or [])]
                processes = value.get("Processes") or []
                lines = ["  ".join(titles)] if titles else []
                lines.extend("  ".join(str(cell) for cell in row) for row in processes)
                self.detail_lines = lines or ["(no processes)"]
            else:
                self.detail_lines = json.dumps(value, indent=2, ensure_ascii=False).splitlines()
            self.detail_mode = "top"
            if not silent:
                self.detail_scroll = 0
            if not silent:
                self.status = f"Top: {item.name}"
        except PodmanError as exc:
            self.status = str(exc)

    def detail_tabs(self) -> tuple[str, ...]:
        item = self.current
        if item and item.kind == "container":
            return ("summary", "logs", "stats", "env", "config", "top")
        if item and item.kind == "pod":
            return ("summary", "stats", "config")
        return ("summary", "config")

    def cycle_detail_tab(self, delta: int) -> None:
        tabs = self.detail_tabs()
        current_index = tabs.index(self.detail_mode) if self.detail_mode in tabs else 0
        next_tab = tabs[(current_index + delta) % len(tabs)]
        if next_tab == "summary":
            self.detail_mode = "summary"
            self.load_summary()
        elif next_tab == "logs":
            self.load_logs()
        elif next_tab == "stats":
            self.load_stats()
        elif next_tab == "env":
            self.load_env()
        elif next_tab == "config":
            self.load_inspect(detail_mode="config")
        elif next_tab == "top":
            self.load_top()

    def focus_main(self) -> None:
        if not self.current:
            self.status = "No item selected."
            return
        self.load_detail()
        self.focus_detail()

    def focus_detail(self) -> None:
        if not self.current:
            self.status = "No item selected."
            return
        self.focus = "main"
        self.detail_scroll = 0

    def return_to_panels(self) -> None:
        self.focus = "side"

    def scroll_detail(self, delta: int) -> None:
        if not self.detail_lines:
            return
        maximum = max(0, len(self.detail_lines) - self.detail_view_rows)
        self.detail_scroll = max(0, min(maximum, self.detail_scroll + delta))

    def page_detail(self, direction: int) -> None:
        self.scroll_detail(direction * max(1, self.detail_view_rows - 1))

    def jump_detail(self, end: bool = False) -> None:
        maximum = max(0, len(self.detail_lines) - self.detail_view_rows)
        self.detail_scroll = maximum if end else 0

    def menu_entries(self) -> list[tuple[str, str]]:
        item = self.current
        if not item:
            return [("Refresh", "refresh")]
        if item.kind == "container":
            pause_action = "unpause" if item.state.lower() == "paused" else "pause"
            pause_label = "Unpause" if pause_action == "unpause" else "Pause"
            return [
                ("Start [S]", "start"),
                ("Stop [s]", "stop"),
                ("Restart [r]", "restart"),
                (f"{pause_label} [p]", pause_action),
                ("Kill [K]", "kill"),
                ("Logs [m]", "logs"),
                ("Attach [a]", "attach"),
                ("Stats [t]", "stats"),
                ("Environment", "env"),
                ("Config [i]", "config"),
                ("Top", "top"),
                ("Exec shell [E]", "shell"),
                ("Hide stopped [e]", "hide_stopped"),
                ("Remove [d]", "remove"),
            ]
        if item.kind == "pod":
            return [
                ("Start [S]", "start"),
                ("Stop [s]", "stop"),
                ("Restart [r]", "restart"),
                ("Pause [p]", "pause"),
                ("Unpause [p]", "unpause"),
                ("Kill [K]", "kill"),
                ("Stats [t]", "stats"),
                ("Config [i]", "config"),
                ("Remove [d]", "remove"),
            ]
        return [("Config [i]", "config"), ("Remove [d]", "remove")]

    def open_menu(self) -> None:
        self.menu_open = True
        self.menu_index = 0

    def close_menu(self) -> None:
        self.menu_open = False

    def move_menu(self, delta: int) -> None:
        entries = self.menu_entries()
        if entries:
            self.menu_index = max(0, min(len(entries) - 1, self.menu_index + delta))

    def selected_menu_action(self) -> str | None:
        entries = self.menu_entries()
        return entries[self.menu_index][1] if entries else None

    def action(self, action: str) -> None:
        item = self.current
        if not item:
            self.status = "No item selected."
            return
        try:
            if item.kind == "container":
                self.client.container_action(item.item_id, action)
            elif item.kind == "pod":
                self.client.pod_action(item.item_id, action)
            elif action == "remove" and item.kind == "image":
                self.client.remove_image(item.item_id)
            elif action == "remove" and item.kind == "volume":
                self.client.remove_volume(item.item_id)
            elif action == "remove" and item.kind == "network":
                self.client.remove_network(item.item_id)
            else:
                self.status = f"Action '{action}' is not available for {item.kind}."
                return
            self.refresh(keep_id=item.item_id)
            if self.status.startswith("Updated "):
                self.status = f"{action.title()} {item.name}: OK"
        except PodmanError as exc:
            self.status = str(exc)

    def move(self, delta: int) -> None:
        if self.items:
            self.selected = max(0, min(len(self.items) - 1, self.selected + delta))
            self.detail_mode = "summary"
            self.load_summary()

    def toggle_hide_stopped(self) -> None:
        self.hide_stopped = not self.hide_stopped
        self.refresh(keep_id=self.current.item_id if self.current else None)
        self.status = "Stopped containers hidden." if self.hide_stopped else "Stopped containers shown."

    def move_focus(self, delta: int) -> None:
        current_index = RESOURCE_MODES.index(self.mode)
        self.mode = RESOURCE_MODES[(current_index + delta) % len(RESOURCE_MODES)]
        self.detail_mode = "summary"
        self.load_summary()

    def toggle_mode(self, mode: str) -> None:
        self.focus = "side"
        if mode == self.mode:
            return
        self.mode = mode
        self.detail_mode = "summary"
        self.load_summary()

    def load_detail(self) -> None:
        if self.mode == "containers":
            self.load_logs()
        else:
            self.load_inspect()

    def exec_shell(self, stdscr: Any) -> None:
        item = self.current
        if not item or item.kind != "container":
            self.status = "A shell is only available for containers."
            return
        curses.def_prog_mode()
        curses.endwin()
        try:
            subprocess.run(["podman", "exec", "-it", item.name, "sh"], check=False)
        except OSError as exc:
            self.status = f"Cannot open shell: {exc}"
        finally:
            curses.reset_prog_mode()
            stdscr.clear()
            stdscr.refresh()
        self.detail_mode = "summary"
        self.refresh(keep_id=item.item_id)

    def attach(self, stdscr: Any) -> None:
        item = self.current
        if not item or item.kind != "container":
            self.status = "Attach is only available for containers."
            return
        curses.def_prog_mode()
        curses.endwin()
        try:
            subprocess.run(["podman", "attach", item.name], check=False)
        except OSError as exc:
            self.status = f"Cannot attach to container: {exc}"
        finally:
            curses.reset_prog_mode()
            stdscr.clear()
            stdscr.refresh()
        self.detail_mode = "summary"
        self.refresh(keep_id=item.item_id)


DEFAULT_THEME = {
    "background": "#12101c",
    "foreground": "#f0c4a8",
    "accent": "#e15a48",
    "selection": "#2c2438",
    "muted": "#6d5a68",
    "green": "#7e9a6a",
    "red": "#d6453d",
    "cyan": "#4a9bb0",
    "yellow": "#f0b45a",
}


def load_omarchy_theme() -> tuple[str, dict[str, str]]:
    """Load the active Omarchy palette without requiring Omarchy at runtime."""
    theme = dict(DEFAULT_THEME)
    state_path = os.path.expanduser("~/.local/state/omarchy/current/theme.name")
    try:
        with open(state_path, encoding="utf-8") as stream:
            theme_id = stream.read().strip()
    except OSError:
        theme_id = ""
    if not theme_id:
        return "Omarchy", theme

    candidates = [
        os.path.expanduser(f"~/.config/omarchy/themes/{theme_id}/colors.toml"),
        os.path.join(os.environ.get("OMARCHY_PATH", "/usr/share/omarchy"), "themes", theme_id, "colors.toml"),
    ]
    for candidate in candidates:
        try:
            with open(candidate, "rb") as stream:
                values = tomllib.load(stream)
            for key in theme:
                if isinstance(values.get(key), str) and values[key].startswith("#"):
                    theme[key] = values[key]
            return theme_id.replace("-", " ").title(), theme
        except (OSError, tomllib.TOMLDecodeError):
            continue
    return theme_id.replace("-", " ").title(), theme


def hex_rgb(value: str) -> tuple[int, int, int]:
    value = value.removeprefix("#")
    if len(value) != 6:
        return (255, 255, 255)
    return tuple(int(value[index : index + 2], 16) for index in (0, 2, 4))


def nearest_terminal_color(value: str) -> int:
    """Map a theme hex color onto the xterm 256-color palette."""
    red, green, blue = hex_rgb(value)
    palette: list[tuple[int, int, int]] = []
    for red_index in range(6):
        for green_index in range(6):
            for blue_index in range(6):
                palette.append((red_index * 40 + 55 if red_index else 0, green_index * 40 + 55 if green_index else 0, blue_index * 40 + 55 if blue_index else 0))
    palette.extend([(level, level, level) for level in range(8, 239, 10)])
    best_index = min(range(len(palette)), key=lambda index: sum((a - b) ** 2 for a, b in zip((red, green, blue), palette[index])))
    return 16 + best_index


def init_colors(theme: dict[str, str]) -> dict[str, int]:
    if not curses.has_colors():
        return {"normal": curses.A_NORMAL, "title": curses.A_BOLD, "selected": curses.A_REVERSE, "muted": curses.A_DIM, "running": curses.A_BOLD, "stopped": curses.A_DIM, "error": curses.A_BOLD, "border": curses.A_DIM, "key": curses.A_BOLD}
    curses.start_color()
    curses.use_default_colors()
    if curses.COLORS >= 256:
        foreground = nearest_terminal_color(theme["foreground"])
        accent = nearest_terminal_color(theme["accent"])
        selection = nearest_terminal_color(theme["selection"])
        muted = nearest_terminal_color(theme["muted"])
        running = nearest_terminal_color(theme["green"])
        error = nearest_terminal_color(theme["red"])
        yellow = nearest_terminal_color(theme["yellow"])
        curses.init_pair(1, foreground, -1)
        curses.init_pair(2, accent, -1)
        curses.init_pair(3, foreground, selection)
        curses.init_pair(4, muted, -1)
        curses.init_pair(5, running, -1)
        curses.init_pair(6, error, -1)
        curses.init_pair(7, yellow, -1)
        return {"normal": curses.color_pair(1), "title": curses.color_pair(2) | curses.A_BOLD, "selected": curses.color_pair(3), "muted": curses.color_pair(4), "running": curses.color_pair(5) | curses.A_BOLD, "stopped": curses.color_pair(4), "error": curses.color_pair(6) | curses.A_BOLD, "border": curses.color_pair(4), "key": curses.color_pair(7) | curses.A_BOLD}
    curses.init_pair(1, curses.COLOR_WHITE, -1)
    curses.init_pair(2, curses.COLOR_CYAN, -1)
    curses.init_pair(3, curses.COLOR_BLACK, curses.COLOR_CYAN)
    curses.init_pair(4, curses.COLOR_WHITE, -1)
    curses.init_pair(5, curses.COLOR_GREEN, -1)
    curses.init_pair(6, curses.COLOR_RED, -1)
    curses.init_pair(7, curses.COLOR_YELLOW, -1)
    return {"normal": curses.color_pair(1), "title": curses.color_pair(2) | curses.A_BOLD, "selected": curses.color_pair(3), "muted": curses.color_pair(4) | curses.A_DIM, "running": curses.color_pair(5) | curses.A_BOLD, "stopped": curses.color_pair(4) | curses.A_DIM, "error": curses.color_pair(6) | curses.A_BOLD, "border": curses.color_pair(4) | curses.A_DIM, "key": curses.color_pair(7) | curses.A_BOLD}


def put(stdscr: Any, y: int, x: int, text: str, width: int, attr: int = curses.A_NORMAL) -> None:
    if y < 0 or x >= width:
        return
    text = "".join(character for character in text if character == "\t" or ord(character) >= 32)
    try:
        stdscr.addnstr(y, x, text, max(0, width - x), attr)
    except curses.error:
        pass


def draw_box(stdscr: Any, top: int, left: int, height: int, panel_width: int, screen_width: int, attr: int) -> None:
    """Draw a lightweight Unicode panel box within the terminal bounds."""
    if height < 2 or panel_width < 4:
        return
    right = left + panel_width - 1
    put(stdscr, top, left, "┌" + "─" * (panel_width - 2) + "┐", screen_width, attr)
    for row in range(top + 1, top + height - 1):
        put(stdscr, row, left, "│", screen_width, attr)
        put(stdscr, row, right, "│", screen_width, attr)
    put(stdscr, top + height - 1, left, "└" + "─" * (panel_width - 2) + "┘", screen_width, attr)


def draw(stdscr: Any, app: App, colors: dict[str, int], theme_name: str) -> None:
    stdscr.erase()
    height, width = stdscr.getmaxyx()
    if height < 22 or width < 70:
        put(stdscr, 0, 0, "Terminal too small. Minimum size is 70x22.", width, colors["error"])
        stdscr.refresh()
        return

    put(stdscr, 0, 0, " lzpody ", width, colors["title"])
    put(stdscr, 0, 14, f"native Libpod  ·  {theme_name}", width, colors["muted"])
    put(stdscr, 1, max(1, width - 25), "[F5] refresh  [q] quit", width, colors["muted"])
    if app.filter_text:
        put(stdscr, 1, 1, f"Filter: {app.filter_text}", width, colors["muted"])

    content_top = 2
    content_bottom = height - 3
    left_x = 1
    left_width = max(32, width // 3)
    right_x = left_width + 2
    right_width = width - right_x - 1
    left_height = content_bottom - content_top + 1
    panel_height, extra = divmod(left_height, len(RESOURCE_MODES))
    panel_top = content_top

    for index, mode in enumerate(RESOURCE_MODES):
        current_height = panel_height + (1 if index < extra else 0)
        panel_items = app.items_by_mode[mode]
        panel_selected = app.selected_by_mode[mode]
        focused = mode == app.mode and app.focus == "side"
        border_attr = colors["title"] if focused else colors["border"]
        draw_box(stdscr, panel_top, left_x, current_height, left_width, width, border_attr)
        panel_right = left_x + left_width - 1
        title_attr = colors["title"] if focused else colors["muted"]
        title = f"[{index + 1}] {RESOURCE_LABELS[mode]} ({len(panel_items)})"
        put(stdscr, panel_top, left_x + 2, title, panel_right, title_attr)

        visible_rows = max(1, current_height - 2)
        start = max(0, min(panel_selected - visible_rows + 1, len(panel_items) - visible_rows))
        for row, item in enumerate(panel_items[start : start + visible_rows], start=panel_top + 1):
            if item.kind == "image":
                marker = "◆"
            elif item.kind in {"volume", "network"}:
                marker = "◇"
            else:
                marker = "●" if item.state.lower() in {"running", "running (healthy)"} else "○"
            if item.kind == "image":
                short_name = item.name.rsplit("/", 1)[-1]
                label = f"{marker} {short_name}  {item.status or 'unknown size'}"
            elif item.kind == "container":
                label = f"{marker} {item.name}  {container_state_label(item)}  {item.cpu:5.2f}%"
            else:
                label = f"{marker} {item.name}  {item.state}"
            selected = start + row - (panel_top + 1) == panel_selected
            if selected and focused:
                attr = colors["selected"]
            elif item.state.lower() in {"running", "running (healthy)"}:
                attr = colors["running"]
            else:
                attr = colors["stopped"] if not selected else colors["muted"]
            put(stdscr, row, left_x + 1, label, panel_right, attr)
        if not panel_items and current_height >= 4:
            put(stdscr, panel_top + 1, left_x + 2, "(empty)", panel_right, colors["muted"])
        panel_top += current_height

    main_border = colors["title"] if app.focus == "main" else colors["border"]
    draw_box(stdscr, content_top, right_x, content_bottom - content_top + 1, right_width, width, main_border)
    right_inner = right_x + right_width - 1
    detail_top = content_top + 2
    visible_detail_rows = max(1, content_bottom - detail_top)
    app.detail_view_rows = visible_detail_rows
    app.detail_scroll = max(0, min(app.detail_scroll, max(0, len(app.detail_lines) - visible_detail_rows)))
    title = app.current.name if app.current else "Details"
    position = ""
    if app.detail_lines:
        first = app.detail_scroll + 1
        last = min(len(app.detail_lines), app.detail_scroll + visible_detail_rows)
        position = f"  ({first}-{last}/{len(app.detail_lines)})"
    put(stdscr, content_top, right_x + 2, f"{title} [{app.detail_mode}]{position}", right_inner, colors["title"])
    detail_tabs = app.detail_tabs()
    tab_text = "  ".join(
        f"[{tab.title()}]" if tab == app.detail_mode else tab.title() for tab in detail_tabs
    )
    put(stdscr, content_top + 1, right_x + 2, tab_text, right_inner, colors["key"])
    detail_lines = app.detail_lines[app.detail_scroll : app.detail_scroll + visible_detail_rows]
    for offset, line in enumerate(detail_lines):
        put(stdscr, detail_top + offset, right_x + 2, line, right_inner, colors["normal"])

    status_lower = app.status.lower()
    is_error = any(
        marker in status_lower
        for marker in ("error", "cannot", "not found", "invalid", "no item", "api 4", "api 5")
    )
    put(stdscr, height - 2, 1, app.status, width - 2, colors["error"] if is_error else colors["muted"])
    if app.menu_open:
        footer = "↑/↓ j/k select  Enter/Space choose  Esc/q close menu"
    elif app.focus == "main":
        footer = "↑/↓ j/k scroll  PgUp/PgDn Ctrl-U/D  Home/End  [/] tabs  Esc panels  x/? menu  q quit"
    else:
        footer = "←/→ h/l panels  ↑/↓ j/k items  Tab  1-5 focus  Enter main  [/] tabs  x/? menu  / filter  q quit"
    put(stdscr, height - 1, 1, footer, width - 2, colors["key"])
    if app.menu_open:
        draw_menu(stdscr, app, colors, width, height)
    stdscr.refresh()


def draw_menu(stdscr: Any, app: App, colors: dict[str, int], width: int, height: int) -> None:
    entries = app.menu_entries()
    if not entries:
        return
    box_width = min(42, max(24, width - 6))
    box_height = min(len(entries) + 2, max(5, height - 4))
    top = max(1, (height - box_height) // 2)
    left = max(2, (width - box_width) // 2)
    draw_box(stdscr, top, left, box_height, box_width, width, colors["title"])
    put(stdscr, top, left + 2, "Actions", left + box_width - 1, colors["title"])
    visible = max(1, box_height - 2)
    start = max(0, min(app.menu_index - visible + 1, len(entries) - visible))
    for offset, (label, _action) in enumerate(entries[start : start + visible]):
        index = start + offset
        attr = colors["selected"] if index == app.menu_index else colors["normal"]
        put(stdscr, top + 1 + offset, left + 2, label, left + box_width - 1, attr)


def prompt_filter(stdscr: Any, app: App) -> None:
    height, width = stdscr.getmaxyx()
    curses.echo()
    curses.curs_set(1)
    put(stdscr, height - 2, 1, "Filter: ", width - 2, curses.A_BOLD)
    try:
        value = stdscr.getstr(height - 2, 9, max(1, width - 11)).decode("utf-8", errors="replace")
    except curses.error:
        value = app.filter_text
    finally:
        curses.noecho()
        curses.curs_set(0)
    app.filter_text = value
    app.selected = 0
    app.refresh()


def confirm(stdscr: Any, prompt: str, colors: dict[str, int] | None = None) -> bool:
    height, width = stdscr.getmaxyx()
    fallback = {
        "title": curses.A_BOLD,
        "selected": curses.A_REVERSE,
        "normal": curses.A_NORMAL,
        "key": curses.A_BOLD,
    }
    colors = colors or fallback
    box_width = min(max(40, len(prompt) + 8), max(40, width - 6))
    box_height = min(7, max(5, height - 2))
    top = max(1, (height - box_height) // 2)
    left = max(2, (width - box_width) // 2)

    draw_box(stdscr, top, left, box_height, box_width, width, colors["title"])
    fill = " " * max(1, box_width - 2)
    for row in range(top + 1, top + box_height - 1):
        put(stdscr, row, left + 1, fill, left + box_width - 1, colors["selected"])
    put(stdscr, top, left + 2, "Confirm action", left + box_width - 1, colors["title"])
    message = prompt[: max(1, box_width - 6)]
    message_left = left + max(2, (box_width - len(message)) // 2)
    put(stdscr, top + 2, message_left, message, left + box_width - 1, colors["selected"] | curses.A_BOLD)
    controls = "Y/Enter confirm   N/Esc cancel"
    controls_left = left + max(2, (box_width - len(controls)) // 2)
    put(stdscr, top + 4, controls_left, controls, left + box_width - 1, colors["key"])
    stdscr.refresh()

    while True:
        key = stdscr.getch()
        if key in (ord("y"), ord("Y"), curses.KEY_ENTER, 10, 13):
            return True
        if key in (ord("n"), ord("N"), ord("q"), 27):
            return False


def request_action(
    stdscr: Any, app: App, action: str, colors: dict[str, int] | None = None
) -> None:
    if action == "stop":
        confirm_and_action(stdscr, app, action, colors)
        return
    app.action(action)


def confirm_and_action(
    stdscr: Any, app: App, action: str, colors: dict[str, int] | None = None
) -> None:
    item = app.current
    if not item or not confirm(stdscr, f"{action.title()} {item.name}?", colors):
        return
    app.status = f"{action.title()} {item.name}..."
    if callable(app.redraw):
        app.redraw()
    app.action(action)


def execute_menu_action(stdscr: Any, app: App, colors: dict[str, int] | None = None) -> None:
    action = app.selected_menu_action()
    app.close_menu()
    if not action:
        return
    if action == "refresh":
        app.refresh(keep_id=app.current.item_id if app.current else None)
    elif action == "logs":
        app.load_logs()
        app.focus_detail()
    elif action == "stats":
        app.load_stats()
        app.focus_detail()
    elif action == "env":
        app.load_env()
        app.focus_detail()
    elif action == "config":
        app.load_inspect()
        app.focus_detail()
    elif action == "top":
        app.load_top()
        app.focus_detail()
    elif action == "shell":
        app.exec_shell(stdscr)
    elif action == "attach":
        app.attach(stdscr)
    elif action == "hide_stopped":
        app.toggle_hide_stopped()
    elif action == "stop":
        request_action(stdscr, app, action, colors)
    elif action == "remove":
        confirm_and_action(stdscr, app, "remove", colors)
    elif action == "kill":
        confirm_and_action(stdscr, app, "kill", colors)
    else:
        app.action(action)


def run(stdscr: Any) -> None:
    curses.curs_set(0)
    stdscr.keypad(True)
    stdscr.timeout(250)
    theme_name, theme = load_omarchy_theme()
    colors = init_colors(theme)
    app = App(PodmanClient())
    app.redraw = lambda: draw(stdscr, app, colors, theme_name)
    app.refresh()

    while app.running:
        if time.monotonic() - app.last_refresh >= REFRESH_SECONDS:
            app.refresh(keep_id=app.current.item_id if app.current else None)
        draw(stdscr, app, colors, theme_name)
        key = stdscr.getch()
        if key == -1:
            continue
        if app.menu_open:
            if key in (ord("q"), 27):
                app.close_menu()
            elif key in (curses.KEY_DOWN, ord("j")):
                app.move_menu(1)
            elif key in (curses.KEY_UP, ord("k")):
                app.move_menu(-1)
            elif key in (curses.KEY_ENTER, 10, 13, ord(" "), ord("y"), ord("Y")):
                execute_menu_action(stdscr, app, colors)
            continue

        if key in (ord("q"), 3):
            app.running = False
        elif key in (ord("x"), ord("?")):
            app.open_menu()
        elif key == ord("1"):
            app.toggle_mode("containers")
        elif key == ord("2"):
            app.toggle_mode("pods")
        elif key == ord("3"):
            app.toggle_mode("images")
        elif key == ord("4"):
            app.toggle_mode("volumes")
        elif key == ord("5"):
            app.toggle_mode("networks")
        elif app.focus == "main" and key == 27:
            app.return_to_panels()
        elif app.focus == "main" and key in (curses.KEY_DOWN, ord("j")):
            app.scroll_detail(1)
        elif app.focus == "main" and key in (curses.KEY_UP, ord("k")):
            app.scroll_detail(-1)
        elif app.focus == "main" and key in (curses.KEY_NPAGE, 4):
            app.page_detail(1)
        elif app.focus == "main" and key in (curses.KEY_PPAGE, 21):
            app.page_detail(-1)
        elif app.focus == "main" and key == curses.KEY_HOME:
            app.jump_detail()
        elif app.focus == "main" and key == curses.KEY_END:
            app.jump_detail(end=True)
        elif key in (curses.KEY_RIGHT, ord("l")) and app.focus == "side":
            app.move_focus(1)
        elif key in (curses.KEY_LEFT, ord("h")) and app.focus == "side":
            app.move_focus(-1)
        elif key == ord("\t") and app.focus == "side":
            app.move_focus(1)
        elif key == getattr(curses, "KEY_BTAB", -2) and app.focus == "side":
            app.move_focus(-1)
        elif app.focus == "side" and key in (curses.KEY_DOWN, ord("j")):
            app.move(1)
        elif app.focus == "side" and key in (curses.KEY_UP, ord("k")):
            app.move(-1)
        elif key in (curses.KEY_ENTER, 10, 13) and app.focus == "side":
            app.focus_main()
        elif key == ord("]"):
            app.cycle_detail_tab(1)
        elif key == ord("["):
            app.cycle_detail_tab(-1)
        elif key == ord("i"):
            app.load_inspect()
            app.focus_detail()
        elif key == ord("t"):
            app.load_stats()
            app.focus_detail()
        elif key == getattr(curses, "KEY_F5", -5):
            app.refresh(keep_id=app.current.item_id if app.current else None)
        elif key == ord("/") and app.focus == "side":
            prompt_filter(stdscr, app)
        elif key == ord("e") and app.focus == "side" and app.mode == "containers":
            app.toggle_hide_stopped()
        elif key == ord("m") and app.focus == "side" and app.mode == "containers":
            app.load_logs()
            app.focus_detail()
        elif key == ord("a") and app.focus == "side" and app.mode == "containers":
            app.attach(stdscr)
        elif key == ord("E") and app.focus == "side" and app.mode == "containers":
            app.exec_shell(stdscr)
        elif key in LIFECYCLE_KEYS and app.focus == "side":
            request_action(stdscr, app, LIFECYCLE_KEYS[key], colors)
        elif key == ord("p") and app.focus == "side":
            if app.current and app.current.state.lower() == "paused":
                app.action("unpause")
            else:
                app.action("pause")
        elif key == ord("K") and app.focus == "side":
            confirm_and_action(stdscr, app, "kill", colors)
        elif key == ord("d") and app.focus == "side":
            confirm_and_action(stdscr, app, "remove", colors)


def main() -> int:
    try:
        curses.wrapper(run)
    except KeyboardInterrupt:
        return 130
    except Exception as exc:  # Keep startup failures readable outside curses.
        print(f"lzpody: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
