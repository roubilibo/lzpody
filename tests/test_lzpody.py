import unittest
from unittest.mock import patch

from lzpody import (
    LIFECYCLE_KEYS,
    App,
    PodmanClient,
    container_item,
    container_state_label,
    confirm_and_action,
    image_item,
    network_item,
    pod_item,
    request_action,
    health_status,
    stats_payload,
    volume_item,
)


class ModelTests(unittest.TestCase):
    def test_container_names_and_state(self):
        item = container_item({"Id": "abc123", "Names": ["/mysql8"], "State": "running", "Image": "mysql:8.4"})
        self.assertEqual(item.name, "mysql8")
        self.assertEqual(item.state, "running")
        self.assertEqual(item.image, "mysql:8.4")

    def test_container_state_label_includes_exit_code(self):
        exited = container_item({"Id": "c1", "Names": ["demo"], "State": "exited", "Status": "Exited (0) 2 seconds ago"})
        self.assertEqual(container_state_label(exited), "exited (0)")

    def test_container_ports_include_forwarding(self):
        item = container_item(
            {
                "Id": "c1",
                "Names": ["mysql8"],
                "Ports": [
                    {"host_ip": "127.0.0.1", "host_port": 3306, "container_port": 3306, "protocol": "tcp"},
                    {"container_port": 33060, "protocol": "tcp"},
                ],
            }
        )
        self.assertEqual(item.ports, ["127.0.0.1:3306->3306/tcp", "33060/tcp"])

    def test_container_health_is_available_for_summary(self):
        self.assertEqual(
            health_status({"Config": {"Healthcheck": None}, "State": {"Status": "exited"}}),
            "no healthcheck",
        )

    def test_pod_summary(self):
        item = pod_item({"Id": "pod123", "Name": "dev", "Status": "Running", "Containers": [{"Id": "c1"}]})
        self.assertEqual(item.name, "dev")
        self.assertEqual(item.status, "1 containers")

    def test_image_summary(self):
        item = image_item({"Id": "sha256:abc", "RepoTags": ["docker.io/library/mysql:8.4"], "Size": 2048})
        self.assertEqual(item.name, "docker.io/library/mysql:8.4")
        self.assertEqual(item.status, "2.0 KiB")

    def test_volume_summary(self):
        item = volume_item({"Name": "mysql-data", "Driver": "local"})
        self.assertEqual(item.name, "mysql-data")
        self.assertEqual(item.status, "local")

    def test_network_summary_accepts_libpod_fields(self):
        item = network_item({"name": "podman", "driver": "bridge"})
        self.assertEqual(item.name, "podman")
        self.assertEqual(item.status, "bridge")


class ActionTests(unittest.TestCase):
    def setUp(self):
        self.client = PodmanClient("/tmp/unused-lzpody.sock")
        self.calls = []
        self.client._request = self.record_request

    def record_request(self, method, path, query=None, body=None):
        self.calls.append((method, path, query))

    def test_container_pause_and_kill_use_native_endpoints(self):
        self.client.container_action("container-id", "pause")
        self.client.container_action("container-id", "kill")
        self.assertEqual(
            self.calls,
            [
                ("POST", "/v5.0.0/libpod/containers/container-id/pause", None),
                ("POST", "/v5.0.0/libpod/containers/container-id/kill", {"signal": "SIGKILL"}),
            ],
        )

    def test_container_lifecycle_actions_use_native_endpoints(self):
        self.client.container_action("container-id", "start")
        self.client.container_action("container-id", "stop")
        self.client.container_action("container-id", "restart")
        self.assertEqual(
            self.calls,
            [
                ("POST", "/v5.0.0/libpod/containers/container-id/start", None),
                ("POST", "/v5.0.0/libpod/containers/container-id/stop", {"timeout": 10}),
                ("POST", "/v5.0.0/libpod/containers/container-id/restart", {"timeout": 10}),
            ],
        )

    def test_pod_unpause_uses_native_endpoint(self):
        self.client.pod_action("pod-id", "unpause")
        self.assertEqual(self.calls, [("POST", "/v5.0.0/libpod/pods/pod-id/unpause", None)])


class NavigationTests(unittest.TestCase):
    def test_lifecycle_shortcuts_match_lazydocker_convention(self):
        self.assertEqual(
            LIFECYCLE_KEYS,
            {ord("S"): "start", ord("s"): "stop", ord("r"): "restart", ord("R"): "restart"},
        )

    def test_focus_wraps_like_lazydocker_side_panels(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.move_focus(-1)
        self.assertEqual(app.mode, "networks")
        app.move_focus(1)
        self.assertEqual(app.mode, "containers")

    def test_container_detail_tabs_cycle(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        app.client.logs = lambda _container_id: "demo log"
        app.cycle_detail_tab(1)
        self.assertEqual(app.detail_mode, "logs")

    def test_enter_and_escape_switch_main_focus(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        app.client.logs = lambda _container_id: "line 1\nline 2"
        app.focus_main()
        self.assertEqual(app.focus, "main")
        self.assertEqual(app.detail_mode, "logs")
        app.return_to_panels()
        self.assertEqual(app.focus, "side")

    def test_numeric_container_key_returns_focus_from_main_panel(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        app.client.logs = lambda _container_id: "demo log"
        app.focus_main()
        self.assertEqual(app.focus, "main")
        app.toggle_mode("containers")
        self.assertEqual(app.mode, "containers")
        self.assertEqual(app.focus, "side")

    def test_detail_scroll_is_clamped(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.detail_lines = ["one", "two", "three", "four", "five"]
        app.detail_view_rows = 3
        app.scroll_detail(100)
        self.assertEqual(app.detail_scroll, 2)
        app.scroll_detail(-100)
        self.assertEqual(app.detail_scroll, 0)
        app.page_detail(1)
        self.assertEqual(app.detail_scroll, 2)
        app.jump_detail()
        self.assertEqual(app.detail_scroll, 0)
        app.jump_detail(end=True)
        self.assertEqual(app.detail_scroll, 2)

    def test_detail_shortcut_moves_focus_to_scrollable_panel(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        app.detail_lines = [str(index) for index in range(20)]
        app.focus_detail()
        self.assertEqual(app.focus, "main")
        app.detail_view_rows = 5
        app.page_detail(1)
        self.assertEqual(app.detail_scroll, 4)

    def test_container_summary_includes_health_without_list_badge(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        app.client.inspect_container = lambda _container_id: {
            "Config": {"Healthcheck": None},
            "State": {"Status": "exited"},
        }
        app.load_summary()
        self.assertIn("Health:  no healthcheck", app.detail_lines)

    def test_container_action_menu_matches_detail_capabilities(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        actions = dict(app.menu_entries())
        self.assertEqual(actions["Start [S]"], "start")
        self.assertEqual(actions["Stop [s]"], "stop")
        self.assertEqual(actions["Restart [r]"], "restart")
        self.assertEqual(actions["Logs [m]"], "logs")
        self.assertEqual(actions["Attach [a]"], "attach")
        self.assertEqual(actions["Exec shell [E]"], "shell")
        self.assertEqual(actions["Hide stopped [e]"], "hide_stopped")
        self.assertEqual(actions["Remove [d]"], "remove")

    def test_hide_stopped_matches_lazydocker_container_shortcut(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.client.containers = lambda: [
            {"Id": "running", "Names": ["up"], "State": "running"},
            {"Id": "stopped", "Names": ["down"], "State": "exited"},
        ]
        app.client.pods = lambda: []
        app.client.images = lambda: []
        app.client.volumes = lambda: []
        app.client.networks = lambda: []
        app.client.stats = lambda _container_id: {"Stats": [{"CPU": 12.5}]}
        app.refresh()
        self.assertEqual([item.name for item in app.items_by_mode["containers"]], ["up", "down"])
        self.assertEqual({item.cpu for item in app.items_by_mode["containers"]}, {12.5})
        app.toggle_hide_stopped()
        self.assertEqual([item.name for item in app.items_by_mode["containers"]], ["up"])

    def test_refresh_populates_cpu_and_ports(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.client.containers = lambda: [
            {
                "Id": "c1",
                "Names": ["mysql8"],
                "State": "running",
                "Ports": [{"host_ip": "127.0.0.1", "host_port": 3306, "container_port": 3306}],
            }
        ]
        app.client.pods = lambda: []
        app.client.images = lambda: []
        app.client.volumes = lambda: []
        app.client.networks = lambda: []
        app.client.stats = lambda _container_id: {"Stats": [{"CPU": 7.25}]}
        app.refresh()
        item = app.items_by_mode["containers"][0]
        self.assertEqual(item.cpu, 7.25)
        self.assertEqual(item.ports, ["127.0.0.1:3306->3306/tcp"])

    def test_stop_requires_confirmation(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        actions = []
        app.action = actions.append
        with patch("lzpody.confirm", return_value=False) as confirm_mock:
            request_action(None, app, "stop")
        confirm_mock.assert_called_once()
        self.assertEqual(actions, [])
        with patch("lzpody.confirm", return_value=True):
            request_action(None, app, "stop")
        self.assertEqual(actions, ["stop"])

    def test_confirmed_action_redraws_before_slow_request(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        events = []
        app.redraw = lambda: events.append(("redraw", app.status))
        app.action = lambda action: events.append(("action", action))
        with patch("lzpody.confirm", return_value=True):
            confirm_and_action(None, app, "stop")
        self.assertEqual(events, [("redraw", "Stop demo..."), ("action", "stop")])

    def test_stats_normalizes_podman_wrapper_and_renders_graphs(self):
        app = App(PodmanClient("/tmp/unused-lzpody.sock"))
        app.items_by_mode["containers"] = [container_item({"Id": "c1", "Names": ["demo"]})]
        samples = iter(
            [
                {"CPU": 10.0, "MemPerc": "20.00%", "MemUsage": 1024, "MemLimit": 4096, "PIDs": 2},
                {"CPU": 30.0, "MemPerc": "40.00%", "MemUsage": 2048, "MemLimit": 4096, "PIDs": 3},
            ]
        )
        app.client.stats = lambda _container_id: {"Error": None, "Stats": [next(samples)]}
        app.load_stats()
        app.load_stats()
        self.assertEqual(stats_payload({"Stats": [{"CPU": 1}]}), {"CPU": 1})
        self.assertEqual(app.detail_mode, "stats")
        self.assertEqual(len(app.stats_history["c1"]), 2)
        self.assertTrue(any("CPU" in line and ("█" in line or "▁" in line) for line in app.detail_lines))
        self.assertTrue(any("Memory" in line for line in app.detail_lines))


if __name__ == "__main__":
    unittest.main()
