"""End-to-end gRPC tests for gonetem link QoS operations.

The tests drive a running ``gonetem-server`` over gRPC, create a project with
``namespace`` and ``docker.host`` nodes, start it, and verify the kernel qdiscs
with ``tc -j qdisc show`` inside the node network namespaces. End-to-end
behavior is also checked:

* latency with ``ping``: a 5 ms one-way delay yields a ~10 ms RTT, before and
  after a LinkUpdate, and on a link created with LinkAdd;
* rate with ``iperf3``: throughput is capped by the configured tbf rate;
* loss with ``ping`` and UDP ``iperf3``: a 10% per-direction loss yields ~19%
  round-trip loss for ping, while one-way UDP traffic sees ~10% loss. Both are
  checked before and after a LinkUpdate.

Every qdisc, RTT, loss and throughput result is printed as a colored
Value/Before/After/Expected table. Use ``-s`` with pytest so the tables are
shown while tests pass. Colors are enabled on a terminal, or when
``FORCE_COLOR=1`` is set; set ``NO_COLOR=1`` to disable them.

Usage:
    python3 -m venv .venv
    . .venv/bin/activate
    python -m pip install -r tests/requirements.txt
    GONETEM_GRPC_TARGET=localhost:10110 python -m pytest -s tests/test_grpc_link_qos.py -v
"""

from __future__ import annotations

import atexit
import io
import json
import os
import random
import re
import shutil
import sys
import tarfile
import tempfile
import time
import uuid
from pathlib import Path

import grpc
import pytest
from grpc_tools import protoc

REPO_ROOT = Path(__file__).resolve().parents[1]
PROTO_FILE = REPO_ROOT / "internal" / "proto" / "netem.proto"
DEFAULT_TARGET = "localhost:10110"
TARGET = os.environ.get("GONETEM_GRPC_TARGET", DEFAULT_TARGET)

_USE_COLOR = os.environ.get("NO_COLOR") is None and (
    sys.stdout.isatty()
    or os.environ.get("FORCE_COLOR", "").lower() not in ("", "0")
)
_ANSI_RESET = "\033[0m"
_ANSI_TITLE = "\033[1;34m"
_ANSI_HEADER = "\033[1;36m"
_ANSI_BEFORE = "\033[33m"
_ANSI_EXPECTED = "\033[36m"
_ANSI_OK = "\033[1;32m"
_ANSI_FAIL = "\033[1;31m"
_ANSI_BORDER = "\033[2m"


def _paint(text: str, color: str) -> str:
    return f"{color}{text}{_ANSI_RESET}" if _USE_COLOR else text


def _print_table(title: str, rows: list[tuple[str, str, str, str, bool]]) -> None:
    """Print a Value/Before/After/Expected table with colored results."""
    headers = ("Value", "Before", "After", "Expected")
    widths = [len(header) for header in headers]
    for row in rows:
        for idx in range(4):
            widths[idx] = max(widths[idx], len(row[idx]))

    border = "+" + "+".join("-" * (width + 2) for width in widths) + "+"

    def format_line(cells, colors=None) -> str:
        formatted = []
        for idx, cell in enumerate(cells):
            padding = cell.ljust(widths[idx])
            if colors and colors[idx]:
                padding = _paint(padding, colors[idx])
            formatted.append(f" {padding} ")
        return "|" + "|".join(formatted) + "|"

    print(flush=True)
    print(_paint(title, _ANSI_TITLE), flush=True)
    print(_paint(border, _ANSI_BORDER), flush=True)
    print(format_line(headers, [_ANSI_HEADER] * len(headers)), flush=True)
    print(_paint(border, _ANSI_BORDER), flush=True)
    for value, before, after, expected, ok in rows:
        print(
            format_line(
                (value, before, after, expected),
                (None, _ANSI_BEFORE, _ANSI_OK if ok else _ANSI_FAIL, _ANSI_EXPECTED),
            ),
            flush=True,
        )
    print(_paint(border, _ANSI_BORDER), flush=True)


# The QoS values below are deliberately different for each direction so that
# an accidental peer swap in LinkUpdate/LinkAdd is detected.
INITIAL_DELAY_MS = 100
INITIAL_JITTER_MS = 10
INITIAL_LOSS_PCT = 2.0

LATENCY_DELAY_MS = 5
UPDATED_LATENCY_MS = 20
ADDED_LATENCY_MS = 15

RATE_KBPS = 1000
UPDATED_RATE_KBPS = 2000
RATE_DELAY_MS = 20
RATE_BUFFER = 10.0
RATE_BURST = 32000
UPDATED_RATE_BUFFER = 5.0

LOSS_PCT = 10.0
UPDATED_LOSS_PCT = 5.0
# A ping packet crosses the lossy link twice: request and reply.
PING_LOSS_PCT = 100.0 * (1.0 - (1.0 - LOSS_PCT / 100.0) ** 2)
PING_UPDATED_LOSS_PCT = 100.0 * (1.0 - (1.0 - UPDATED_LOSS_PCT / 100.0) ** 2)

UPDATED_PEER1 = {
    "delay": 30,
    "jitter": 3,
    "loss": 1.0,
    "rate": 10_000,  # kbps
    "buffer": 1.0,
    "burst": 5_000,
}
UPDATED_PEER2 = {
    "delay": 60,
    "jitter": 6,
    "loss": 4.0,
    "rate": 2_000,  # kbps
    "buffer": 0.5,
    "burst": 2_000,
}

ADDED_PEER1 = {"delay": 25, "jitter": 2, "loss": 0.5}
ADDED_PEER2 = {"delay": 75, "jitter": 7, "loss": 3.0}


def _generate_stubs() -> None:
    """Generate the Python protobuf/gRPC modules from netem.proto."""
    out_dir = Path(tempfile.mkdtemp(prefix="gonetem-grpc-stubs-"))
    atexit.register(shutil.rmtree, out_dir, ignore_errors=True)

    result = protoc.main(
        [
            "grpc_tools.protoc",
            f"-I{PROTO_FILE.parent}",
            f"--python_out={out_dir}",
            f"--grpc_python_out={out_dir}",
            str(PROTO_FILE),
        ]
    )
    if result != 0:
        raise RuntimeError(f"grpc_tools.protoc failed with exit code {result}")

    sys.path.insert(0, str(out_dir))


_generate_stubs()

import netem_pb2  # noqa: E402
import netem_pb2_grpc  # noqa: E402


NODE_TYPES = ("namespace", "docker.host")

ONE_LINK_LINKS = f"""\
- peer1: n1.0
  peer2: n2.0
  delay: {INITIAL_DELAY_MS}
  jitter: {INITIAL_JITTER_MS}
  loss: {INITIAL_LOSS_PCT}
"""


TWO_LINK_LINKS = f"""\
- peer1: n1.0
  peer2: n2.0
  delay: {INITIAL_DELAY_MS}
  jitter: {INITIAL_JITTER_MS}
  loss: {INITIAL_LOSS_PCT}
- peer1: n1.1
  peer2: n2.1
  delay: 40
  jitter: 4
  loss: 1
"""


LATENCY_LINKS = f"""\
- peer1: n1.0
  peer2: n2.0
  delay: {LATENCY_DELAY_MS}
"""


RATE_LINKS = f"""\
- peer1: n1.0
  peer2: n2.0
  delay: {RATE_DELAY_MS}
  rate: {RATE_KBPS}
  buffer: {RATE_BUFFER}
  burst: {RATE_BURST}
"""


LOSS_LINKS = f"""\
- peer1: n1.0
  peer2: n2.0
  loss: {LOSS_PCT}
"""


def _topology(node_type: str, links: str) -> str:
    return f"""\
nodes:
  n1:
    type: {node_type}
    launch: true
  n2:
    type: {node_type}
    launch: true
links:
{links}"""


def _project_archive(network_yml: str) -> bytes:
    """Build a .gnet project archive (tar.gz containing network.yml)."""
    payload = network_yml.encode()
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as tar:
        info = tarfile.TarInfo("network.yml")
        info.size = len(payload)
        info.mode = 0o644
        info.mtime = int(time.time())
        tar.addfile(info, io.BytesIO(payload))
    return buffer.getvalue()


def _qos(**values) -> netem_pb2.LinkConfig.QoSConfig:
    return netem_pb2.LinkConfig.QoSConfig(**values)


def assert_ack(response) -> None:
    assert response.status.code == netem_pb2.StatusCode.OK, response.status.error


def _netem_options(qdiscs, context: str) -> dict:
    netem = [q for q in qdiscs if q.get("kind") == "netem"]
    assert len(netem) == 1, f"{context}: expected exactly one netem qdisc, got {qdiscs}"
    return netem[0]["options"]


QOS_METRICS = ("Delay", "Jitter", "Loss", "Rate", "Burst")


def _fmt_qos_metric(metric: str, value: float | None) -> str:
    if value is None:
        return "—"
    if metric in ("Delay", "Jitter"):
        return f"{value:.3f} ms"
    if metric == "Loss":
        return f"{value:.3f} %"
    if metric == "Rate":
        return f"{value:.3f} kbps"
    if metric == "Burst":
        return f"{value:.0f} B"
    return str(value)


def _qdisc_metrics(qdiscs, context: str) -> dict[str, float]:
    options = _netem_options(qdiscs, context)
    tbf = [q for q in qdiscs if q.get("kind") == "tbf"]
    tbf_options = tbf[0]["options"] if len(tbf) == 1 else None

    return {
        "Delay": options["delay"]["delay"] * 1000.0,
        "Jitter": options["delay"]["jitter"] * 1000.0,
        "Loss": options["loss-random"]["loss"] * 100.0,
        "Rate": tbf_options["rate"] / 125.0 if tbf_options else 0.0,
        "Burst": float(tbf_options["burst"]) if tbf_options else 0.0,
    }


def _qos_metric_matches(metric: str, actual: float, expected: float) -> bool:
    if metric in ("Delay", "Jitter"):
        return abs(actual - expected) <= 1.0
    if metric == "Loss":
        return abs(actual - expected) <= 0.01
    return actual == expected


def assert_link_qos(qdiscs, *, delay: int, jitter: int, loss: float,
                    rate: int | None = None, burst: int | None = None,
                    before: dict[str, float] | None = None,
                    print_result: bool = True,
                    context: str = "") -> dict[str, float]:
    """Assert the netem (and optional tbf) parameters reported by the kernel."""
    actual = _qdisc_metrics(qdiscs, context)
    expected = {
        "Delay": float(delay),
        "Jitter": float(jitter),
        "Loss": float(loss),
        "Rate": float(rate or 0),
        "Burst": float(burst or 0),
    }

    if print_result:
        rows = []
        for metric in QOS_METRICS:
            before_value = before.get(metric) if before else None
            rows.append(
                (
                    metric,
                    _fmt_qos_metric(metric, before_value),
                    _fmt_qos_metric(metric, actual[metric]),
                    _fmt_qos_metric(metric, expected[metric]),
                    _qos_metric_matches(metric, actual[metric], expected[metric]),
                )
            )
        _print_table(context or "Link QoS", rows)

    assert actual["Delay"] == pytest.approx(
        delay, abs=1.0
    ), f"{context}: unexpected delay in {qdiscs}"
    assert actual["Jitter"] == pytest.approx(
        jitter, abs=1.0
    ), f"{context}: unexpected jitter in {qdiscs}"
    assert actual["Loss"] == pytest.approx(
        loss, abs=0.01
    ), f"{context}: unexpected loss in {qdiscs}"

    tbf = [q for q in qdiscs if q.get("kind") == "tbf"]
    if rate is None:
        assert not tbf, f"{context}: unexpected tbf qdisc in {qdiscs}"
    else:
        assert len(tbf) == 1, f"{context}: expected exactly one tbf qdisc, got {qdiscs}"
        assert actual["Rate"] == rate, f"{context}: unexpected tbf rate in {qdiscs}"
        if burst is not None:
            assert actual["Burst"] == burst, f"{context}: unexpected tbf burst in {qdiscs}"

    return actual


def _run_command(project, node: str, cmd: list[str], context: str):
    response = project.run_node(node, cmd)
    assert response.status.code == netem_pb2.StatusCode.OK, response.status.error
    assert response.exitCode == 0, (
        f"{context} failed on {node}: exit={response.exitCode} "
        f"stdout={response.stdout!r} stderr={response.stderr!r}"
    )
    return response


def _set_ipv4(project, node: str, address: str, interface: str = "eth0") -> None:
    _run_command(
        project,
        node,
        ["ip", "addr", "replace", address, "dev", interface],
        f"ip addr replace {address}",
    )
    _run_command(
        project,
        node,
        ["ip", "link", "set", interface, "up"],
        f"ip link set {interface} up",
    )


PING_RTT_RE = re.compile(
    r"rtt min/avg/max/mdev = [\d.]+/([\d.]+)/[\d.]+/[\d.]+ ms"
)
PING_LOSS_RE = re.compile(r"([\d.]+)% packet loss")


def _ping_average_rtt(project, source: str, destination: str) -> float:
    # Resolve the neighbour entry first: ARP resolution otherwise adds a
    # variable amount of latency to the first echoed packet.
    _run_command(
        project,
        source,
        ["ping", "-c", "1", "-W", "2", destination],
        f"ping {destination} warm-up",
    )

    response = _run_command(
        project,
        source,
        ["ping", "-q", "-c", "5", "-i", "0.1", "-W", "2", destination],
        f"ping {destination}",
    )
    output = response.stdout.decode(errors="replace")
    assert "0% packet loss" in output, f"packet loss while pinging {destination}: {output}"

    match = PING_RTT_RE.search(output)
    assert match is not None, f"unable to parse ping RTT from output: {output!r}"
    return float(match.group(1))


def _ping_loss_percent(project, source: str, destination: str) -> float:
    # Warm up the neighbour cache with several packets because a single packet
    # can be dropped by the loss qdisc.
    _run_command(
        project,
        source,
        ["ping", "-q", "-c", "5", "-i", "0.02", "-W", "2", destination],
        f"ping {destination} loss warm-up",
    )
    response = _run_command(
        project,
        source,
        ["ping", "-q", "-c", "300", "-i", "0.005", "-W", "2", destination],
        f"ping {destination} loss measurement",
    )
    output = response.stdout.decode(errors="replace")
    match = PING_LOSS_RE.search(output)
    assert match is not None, f"unable to parse packet loss from output: {output!r}"
    return float(match.group(1))


def _iperf_port() -> str:
    # A fresh port per measurement avoids racing with a previous one-off
    # iperf3 server that is still shutting down.
    return str(random.randint(20000, 60000))


def _iperf_udp_loss_once(project, source: str, destination: str) -> float:
    server_node = "n2" if source == "n1" else "n1"
    port = _iperf_port()
    _run_command(
        project,
        server_node,
        ["iperf3", "-s", "-1", "-D", "-p", port],
        "start iperf3 UDP server",
    )
    time.sleep(0.3)
    response = _run_command(
        project,
        source,
        ["iperf3", "-c", destination, "-u", "-b", "5M", "-t", "2", "-p", port, "-J"],
        "iperf3 UDP client",
    )
    data = json.loads(response.stdout.decode(errors="replace"))
    return float(data["end"]["sum"]["lost_percent"])


def _iperf_udp_loss_percent(project, source: str, destination: str) -> float:
    last_error = None
    for _ in range(3):
        try:
            return _iperf_udp_loss_once(project, source, destination)
        except Exception as exc:  # noqa: BLE001 - retry flaky iperf3 control sockets
            last_error = exc
            time.sleep(0.5)
    raise AssertionError(
        f"iperf3 UDP loss measurement failed after 3 attempts: {last_error}"
    ) from last_error


def _iperf_rate_once(project, source: str, destination: str) -> float:
    server_node = "n2" if source == "n1" else "n1"
    port = _iperf_port()
    _run_command(
        project,
        server_node,
        ["iperf3", "-s", "-1", "-D", "-p", port],
        "start iperf3 server",
    )
    time.sleep(0.3)
    response = _run_command(
        project,
        source,
        ["iperf3", "-c", destination, "-t", "2", "-p", port, "-J"],
        "iperf3 client",
    )
    data = json.loads(response.stdout.decode(errors="replace"))
    bits_per_second = data["end"]["sum_received"]["bits_per_second"]
    return float(bits_per_second) / 1e6


def _iperf_rate_mbps(project, source: str, destination: str) -> float:
    last_error = None
    for _ in range(3):
        try:
            return _iperf_rate_once(project, source, destination)
        except Exception as exc:  # noqa: BLE001 - retry flaky iperf3 control sockets
            last_error = exc
            time.sleep(0.5)
    raise AssertionError(
        f"iperf3 rate measurement failed after 3 attempts: {last_error}"
    ) from last_error


def _fmt_ms(value: float | None) -> str:
    return "—" if value is None else f"{value:.3f} ms"


def _fmt_percent(value: float | None) -> str:
    return "—" if value is None else f"{value:.3f} %"


def _fmt_mbps(value: float | None) -> str:
    return "—" if value is None else f"{value:.3f} Mbps"


def check_ping_rtts(project, title: str, checks, before=None) -> dict[tuple[str, str], float]:
    results = {}
    rows = []
    for source, destination, expected in checks:
        average = _ping_average_rtt(project, source, destination)
        key = (source, destination)
        results[key] = average
        before_value = before.get(key) if before else None
        rows.append(
            (
                f"RTT {source} -> {destination}",
                _fmt_ms(before_value),
                _fmt_ms(average),
                _fmt_ms(expected),
                abs(average - expected) <= 2.0,
            )
        )

    _print_table(title, rows)

    for source, destination, expected in checks:
        average = results[(source, destination)]
        assert average == pytest.approx(expected, abs=2.0), (
            f"{title}: unexpected RTT when pinging {destination} from {source}: "
            f"average={average:.3f}ms expected={expected:.3f}ms"
        )
    return results


def check_ping_losses(project, title: str, checks, before=None,
                      tolerance: float = 7.0) -> dict[tuple[str, str], float]:
    results = {}
    rows = []
    for source, destination, expected in checks:
        loss = _ping_loss_percent(project, source, destination)
        key = (source, destination)
        results[key] = loss
        before_value = before.get(key) if before else None
        rows.append(
            (
                f"Loss {source} -> {destination}",
                _fmt_percent(before_value),
                _fmt_percent(loss),
                _fmt_percent(expected),
                abs(loss - expected) <= tolerance,
            )
        )

    _print_table(title, rows)

    for source, destination, expected in checks:
        loss = results[(source, destination)]
        assert loss == pytest.approx(expected, abs=tolerance), (
            f"{title}: unexpected packet loss when pinging {destination} "
            f"from {source}: measured={loss:.3f}% expected={expected:.3f}%"
        )
    return results


def check_udp_losses(project, title: str, checks, before=None,
                     tolerance: float = 5.0) -> dict[tuple[str, str], float]:
    results = {}
    rows = []
    for source, destination, expected in checks:
        loss = _iperf_udp_loss_percent(project, source, destination)
        key = (source, destination)
        results[key] = loss
        before_value = before.get(key) if before else None
        rows.append(
            (
                f"UDP loss {source} -> {destination}",
                _fmt_percent(before_value),
                _fmt_percent(loss),
                _fmt_percent(expected),
                abs(loss - expected) <= tolerance,
            )
        )

    _print_table(title, rows)

    for source, destination, expected in checks:
        loss = results[(source, destination)]
        assert loss == pytest.approx(expected, abs=tolerance), (
            f"{title}: unexpected one-way UDP loss from {source} to "
            f"{destination}: measured={loss:.3f}% expected={expected:.3f}%"
        )
    return results


def check_rate(project, title: str, source: str, destination: str,
               expected_mbps: float, before: float | None = None) -> float:
    actual = _iperf_rate_mbps(project, source, destination)
    tolerance = max(0.3, expected_mbps * 0.2)
    rows = [
        (
            "Throughput",
            _fmt_mbps(before),
            _fmt_mbps(actual),
            _fmt_mbps(expected_mbps),
            abs(actual - expected_mbps) <= tolerance,
        )
    ]

    _print_table(title, rows)

    assert actual == pytest.approx(expected_mbps, abs=tolerance), (
        f"{title}: unexpected TCP throughput from {source} to {destination}: "
        f"measured={actual:.3f}Mbps expected={expected_mbps:.3f}Mbps"
    )
    return actual


class GonetemProject:
    def __init__(self, stub, prj_id: str, node_type: str):
        self.stub = stub
        self.prj_id = prj_id
        self.node_type = node_type

    def run(self) -> None:
        for _ in self.stub.TopologyRun(netem_pb2.ProjectRequest(id=self.prj_id)):
            pass

    def run_node(self, node: str, cmd: list[str]):
        return self.stub.NodeRun(
            netem_pb2.NodeRunRequest(prjId=self.prj_id, node=node, cmd=cmd)
        )

    def qdiscs(self, node: str, interface: str) -> list[dict]:
        response = self.run_node(
            node, ["tc", "-j", "qdisc", "show", "dev", interface]
        )
        assert response.status.code == netem_pb2.StatusCode.OK, response.status.error
        assert response.exitCode == 0, (
            f"tc qdisc show failed on {node}/{interface}: "
            f"exit={response.exitCode} stderr={response.stderr!r}"
        )
        return json.loads(response.stdout)

    def link_update(self, link) -> None:
        assert_ack(
            self.stub.LinkUpdate(
                netem_pb2.LinkRequest(prjId=self.prj_id, link=link, sync=True)
            )
        )

    def link_add(self, link) -> None:
        assert_ack(
            self.stub.LinkAdd(
                netem_pb2.LinkRequest(prjId=self.prj_id, link=link, sync=True)
            )
        )

    def link_del(self, peer1: str, peer2: str) -> None:
        assert_ack(
            self.stub.LinkDel(
                netem_pb2.LinkRequest(
                    prjId=self.prj_id,
                    link=netem_pb2.LinkConfig(peer1=peer1, peer2=peer2),
                    sync=True,
                )
            )
        )

    def close(self) -> None:
        try:
            for _ in self.stub.ProjectClose(netem_pb2.ProjectRequest(id=self.prj_id)):
                pass
        except grpc.RpcError:
            # Cleanup must not hide the actual test failure.
            pass


@pytest.fixture(scope="session")
def stub():
    channel = grpc.insecure_channel(TARGET)
    try:
        grpc.channel_ready_future(channel).result(timeout=10)
    except grpc.FutureTimeoutError:
        channel.close()
        pytest.fail(
            f"unable to connect to gonetem-server at {TARGET}; "
            "start it or set GONETEM_GRPC_TARGET"
        )

    yield netem_pb2_grpc.NetemStub(channel)
    channel.close()


@pytest.fixture(params=NODE_TYPES, ids=NODE_TYPES)
def project_factory(request, stub):
    node_type = request.param
    projects = []

    def open_project(links: str) -> GonetemProject:
        name = f"grpc-link-qos-{uuid.uuid4().hex}.gnet"
        response = stub.ProjectOpen(
            netem_pb2.OpenRequest(
                name=name,
                data=_project_archive(_topology(node_type, links)),
            )
        )
        assert_ack(response)

        project = GonetemProject(stub, response.id, node_type)
        projects.append(project)
        project.run()
        print(
            f"\n[project] id={project.prj_id} node_type={node_type} started",
            flush=True,
        )
        return project

    yield open_project

    for project in reversed(projects):
        project.close()


def test_link_update_changes_qdisc_parameters(project_factory):
    project = project_factory(ONE_LINK_LINKS)

    before = {}
    for node in ("n1", "n2"):
        before[node] = assert_link_qos(
            project.qdiscs(node, "eth0"),
            delay=INITIAL_DELAY_MS,
            jitter=INITIAL_JITTER_MS,
            loss=INITIAL_LOSS_PCT,
            print_result=False,
            context=f"{project.node_type} {node}/eth0 before LinkUpdate",
        )

    project.link_update(
        netem_pb2.LinkConfig(
            peer1="n1.0",
            peer2="n2.0",
            peer1qos=_qos(**UPDATED_PEER1),
            peer2qos=_qos(**UPDATED_PEER2),
        )
    )

    assert_link_qos(
        project.qdiscs("n1", "eth0"),
        delay=UPDATED_PEER1["delay"],
        jitter=UPDATED_PEER1["jitter"],
        loss=UPDATED_PEER1["loss"],
        rate=UPDATED_PEER1["rate"],
        burst=UPDATED_PEER1["burst"],
        before=before["n1"],
        context=f"LinkUpdate - {project.node_type} - n1/eth0",
    )
    assert_link_qos(
        project.qdiscs("n2", "eth0"),
        delay=UPDATED_PEER2["delay"],
        jitter=UPDATED_PEER2["jitter"],
        loss=UPDATED_PEER2["loss"],
        rate=UPDATED_PEER2["rate"],
        burst=UPDATED_PEER2["burst"],
        before=before["n2"],
        context=f"LinkUpdate - {project.node_type} - n2/eth0",
    )


def test_link_latency_is_reflected_in_ping_rtt(project_factory):
    project = project_factory(LATENCY_LINKS)

    _set_ipv4(project, "n1", "10.10.0.1/24")
    _set_ipv4(project, "n2", "10.10.0.2/24")

    checks = [
        ("n1", "10.10.0.2", 2 * LATENCY_DELAY_MS),
        ("n2", "10.10.0.1", 2 * LATENCY_DELAY_MS),
    ]
    initial = check_ping_rtts(
        project,
        f"Latency - {project.node_type} - initial link",
        checks,
    )

    project.link_update(
        netem_pb2.LinkConfig(
            peer1="n1.0",
            peer2="n2.0",
            peer1qos=_qos(delay=UPDATED_LATENCY_MS),
            peer2qos=_qos(delay=UPDATED_LATENCY_MS),
        )
    )

    check_ping_rtts(
        project,
        f"Latency - {project.node_type} - after LinkUpdate",
        [
            ("n1", "10.10.0.2", 2 * UPDATED_LATENCY_MS),
            ("n2", "10.10.0.1", 2 * UPDATED_LATENCY_MS),
        ],
        before=initial,
    )

    project.link_add(
        netem_pb2.LinkConfig(
            peer1="n1.1",
            peer2="n2.1",
            peer1qos=_qos(delay=ADDED_LATENCY_MS),
            peer2qos=_qos(delay=ADDED_LATENCY_MS),
        )
    )

    _set_ipv4(project, "n1", "10.10.1.1/24", interface="eth1")
    _set_ipv4(project, "n2", "10.10.1.2/24", interface="eth1")

    check_ping_rtts(
        project,
        f"Latency - {project.node_type} - after LinkAdd",
        [
            ("n1", "10.10.1.2", 2 * ADDED_LATENCY_MS),
            ("n2", "10.10.1.1", 2 * ADDED_LATENCY_MS),
        ],
    )


def test_link_rate_is_enforced(project_factory):
    project = project_factory(RATE_LINKS)

    _set_ipv4(project, "n1", "10.10.0.1/24")
    _set_ipv4(project, "n2", "10.10.0.2/24")

    initial = check_rate(
        project,
        f"Rate - {project.node_type} - initial link",
        "n1",
        "10.10.0.2",
        RATE_KBPS / 1000.0,
    )

    project.link_update(
        netem_pb2.LinkConfig(
            peer1="n1.0",
            peer2="n2.0",
            peer1qos=_qos(
                delay=RATE_DELAY_MS,
                rate=UPDATED_RATE_KBPS,
                buffer=UPDATED_RATE_BUFFER,
                burst=RATE_BURST,
            ),
            peer2qos=_qos(
                delay=RATE_DELAY_MS,
                rate=UPDATED_RATE_KBPS,
                buffer=UPDATED_RATE_BUFFER,
                burst=RATE_BURST,
            ),
        )
    )

    check_rate(
        project,
        f"Rate - {project.node_type} - after LinkUpdate",
        "n1",
        "10.10.0.2",
        UPDATED_RATE_KBPS / 1000.0,
        before=initial,
    )


def test_link_loss_is_reflected_in_traffic(project_factory):
    project = project_factory(LOSS_LINKS)

    _set_ipv4(project, "n1", "10.10.0.1/24")
    _set_ipv4(project, "n2", "10.10.0.2/24")

    ping_checks = [
        ("n1", "10.10.0.2", PING_LOSS_PCT),
        ("n2", "10.10.0.1", PING_LOSS_PCT),
    ]
    udp_checks = [
        ("n1", "10.10.0.2", LOSS_PCT),
        ("n2", "10.10.0.1", LOSS_PCT),
    ]

    initial_ping = check_ping_losses(
        project,
        f"Ping loss - {project.node_type} - initial link",
        ping_checks,
    )
    initial_udp = check_udp_losses(
        project,
        f"UDP loss - {project.node_type} - initial link",
        udp_checks,
    )

    project.link_update(
        netem_pb2.LinkConfig(
            peer1="n1.0",
            peer2="n2.0",
            peer1qos=_qos(loss=UPDATED_LOSS_PCT),
            peer2qos=_qos(loss=UPDATED_LOSS_PCT),
        )
    )

    check_ping_losses(
        project,
        f"Ping loss - {project.node_type} - after LinkUpdate",
        [
            ("n1", "10.10.0.2", PING_UPDATED_LOSS_PCT),
            ("n2", "10.10.0.1", PING_UPDATED_LOSS_PCT),
        ],
        before=initial_ping,
    )
    check_udp_losses(
        project,
        f"UDP loss - {project.node_type} - after LinkUpdate",
        [
            ("n1", "10.10.0.2", UPDATED_LOSS_PCT),
            ("n2", "10.10.0.1", UPDATED_LOSS_PCT),
        ],
        before=initial_udp,
    )


def test_link_add_creates_link_with_requested_parameters(project_factory):
    project = project_factory(ONE_LINK_LINKS)

    # The new interfaces must not exist before LinkAdd.
    for node in ("n1", "n2"):
        response = project.run_node(
            node, ["tc", "-j", "qdisc", "show", "dev", "eth1"]
        )
        assert response.status.code == netem_pb2.StatusCode.OK, response.status.error
        assert response.exitCode != 0, f"{node}/eth1 unexpectedly exists before LinkAdd"

    project.link_add(
        netem_pb2.LinkConfig(
            peer1="n1.1",
            peer2="n2.1",
            peer1qos=_qos(**ADDED_PEER1),
            peer2qos=_qos(**ADDED_PEER2),
        )
    )

    assert_link_qos(
        project.qdiscs("n1", "eth1"),
        delay=ADDED_PEER1["delay"],
        jitter=ADDED_PEER1["jitter"],
        loss=ADDED_PEER1["loss"],
        context=f"LinkAdd - {project.node_type} - n1/eth1",
    )
    assert_link_qos(
        project.qdiscs("n2", "eth1"),
        delay=ADDED_PEER2["delay"],
        jitter=ADDED_PEER2["jitter"],
        loss=ADDED_PEER2["loss"],
        context=f"LinkAdd - {project.node_type} - n2/eth1",
    )


def test_link_del_removes_link_and_qdiscs(project_factory):
    project = project_factory(TWO_LINK_LINKS)

    before_eth0 = {}
    for node in ("n1", "n2"):
        assert_link_qos(
            project.qdiscs(node, "eth1"),
            delay=40,
            jitter=4,
            loss=1.0,
            context=f"LinkDel - {project.node_type} - {node}/eth1 before",
        )
        before_eth0[node] = assert_link_qos(
            project.qdiscs(node, "eth0"),
            delay=INITIAL_DELAY_MS,
            jitter=INITIAL_JITTER_MS,
            loss=INITIAL_LOSS_PCT,
            print_result=False,
            context=f"LinkDel - {project.node_type} - {node}/eth0 before",
        )

    project.link_del("n1.1", "n2.1")

    for node in ("n1", "n2"):
        response = project.run_node(
            node, ["tc", "-j", "qdisc", "show", "dev", "eth1"]
        )
        assert response.status.code == netem_pb2.StatusCode.OK, response.status.error
        assert response.exitCode != 0, (
            f"{node}/eth1 still exists after LinkDel: {response.stdout!r}"
        )
        _print_table(
            f"LinkDel - {project.node_type} - {node}/eth1",
            [("Interface eth1", "present", "absent", "absent", True)],
        )

    # The first link must not have been affected by the deletion.
    for node in ("n1", "n2"):
        assert_link_qos(
            project.qdiscs(node, "eth0"),
            delay=INITIAL_DELAY_MS,
            jitter=INITIAL_JITTER_MS,
            loss=INITIAL_LOSS_PCT,
            before=before_eth0[node],
            context=f"LinkDel - {project.node_type} - {node}/eth0 after",
        )


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-v", "-s"]))
