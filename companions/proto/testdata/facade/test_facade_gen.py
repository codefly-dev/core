"""End-to-end test of the facade generator against a real protoc descriptor.

Compiles the fixture proto with `protoc`, feeds the descriptor to the generator,
then imports the generated facade alongside the generated `*_pb2` modules and
drives a call through a fake gateway — proving the emitted procedure string,
request pass-through, and response type are correct.
"""

from __future__ import annotations

import importlib
import io
import shutil
import subprocess
import sys
from pathlib import Path

import pytest
from google.protobuf.compiler import plugin_pb2
from google.protobuf.descriptor_pb2 import FileDescriptorProto, FileDescriptorSet

import protoc_gen_codefly_facade_python as facade_gen

FIXTURES = Path(__file__).parent
PROTO = "saas/accounts/v1/audit.proto"

pytestmark = pytest.mark.skipif(shutil.which("protoc") is None, reason="protoc not installed")


def _descriptor_set(tmp_path: Path) -> FileDescriptorSet:
    out = tmp_path / "descriptors.pb"
    subprocess.run(
        ["protoc", "-I", str(FIXTURES), "--include_imports", f"--descriptor_set_out={out}", PROTO],
        check=True,
    )
    return FileDescriptorSet.FromString(out.read_bytes())


def _request(tmp_path: Path, parameter: str) -> plugin_pb2.CodeGeneratorRequest:
    request = plugin_pb2.CodeGeneratorRequest()
    request.proto_file.extend(_descriptor_set(tmp_path).file)
    request.file_to_generate.append(PROTO)
    request.parameter = parameter
    return request


def _compile_pb2(tmp_path: Path) -> Path:
    root = tmp_path / "sdk"
    root.mkdir()
    subprocess.run(
        ["protoc", "-I", str(FIXTURES), f"--python_out={root}", f"--pyi_out={root}", PROTO],
        check=True,
    )
    for package in ("saas", "saas/accounts", "saas/accounts/v1"):
        (root / package / "__init__.py").touch()
    return root


class FakeGateway:
    def __init__(self, response):
        self._response = response
        self.calls: list[tuple] = []

    def unary(self, procedure, request, response_type):
        self.calls.append((procedure, request, response_type))
        return self._response


def _load_facade(root: Path, name: str):
    sys.path.insert(0, str(root))
    try:
        for mod in list(sys.modules):
            if mod == name or mod.startswith("saas"):
                del sys.modules[mod]
        return importlib.import_module(name)
    finally:
        sys.path.remove(str(root))


def test_generates_facade_for_every_service(tmp_path):
    content = facade_gen.generate(_request(tmp_path, "module=accounts")).file[0].content
    assert "def accounts(gateway):" in content
    assert "class AuditServiceClient:" in content
    assert "class IdentityServiceClient:" in content
    assert "def audit(self):" in content
    assert "def identity(self):" in content
    assert "def query_audit_log(self, request):" in content
    assert '"/saas.accounts.v1.AuditService/QueryAuditLog"' in content


def test_declared_subset_drops_other_services(tmp_path):
    content = facade_gen.generate(
        _request(tmp_path, "module=accounts,services=AuditService")
    ).file[0].content
    assert "class AuditServiceClient:" in content
    assert "IdentityService" not in content


def test_facade_routes_through_gateway(tmp_path):
    root = _compile_pb2(tmp_path)
    facade = facade_gen.generate(_request(tmp_path, "module=accounts")).file[0]
    (root / facade.name).write_text(facade.content)

    accounts = _load_facade(root, "accounts")
    audit_pb2 = importlib.import_module("saas.accounts.v1.audit_pb2")

    expected = audit_pb2.QueryAuditLogResponse(next_page_token="cursor")
    gateway = FakeGateway(expected)
    request = audit_pb2.QueryAuditLogRequest(page_size=20)

    result = accounts.accounts(gateway).audit().query_audit_log(request)

    assert result is expected
    (procedure, sent, response_type) = gateway.calls[0]
    assert procedure == "/saas.accounts.v1.AuditService/QueryAuditLog"
    assert sent is request
    assert response_type is audit_pb2.QueryAuditLogResponse


# --- descriptor-level tests (no protoc needed) -------------------------------


def _file(name, package, messages, services):
    fd = FileDescriptorProto(name=name, package=package)
    for message in messages:
        fd.message_type.add(name=message)
    for service_name, methods in services:
        service = fd.service.add(name=service_name)
        for method_name, input_type, output_type in methods:
            service.method.add(name=method_name, input_type=input_type, output_type=output_type)
    return fd


def _desc_request(files, generate, parameter=""):
    request = plugin_pb2.CodeGeneratorRequest(parameter=parameter)
    request.proto_file.extend(files)
    request.file_to_generate.extend(generate)
    return request


def test_declared_service_typo_fails_loud():
    fd = _file(
        "a.proto",
        "svc.v1",
        ["Req", "Resp"],
        [("AuditService", [("Do", ".svc.v1.Req", ".svc.v1.Resp")])],
    )
    with pytest.raises(ValueError, match="declared services not found"):
        facade_gen.generate(_desc_request([fd], ["a.proto"], "services=AudtiService"))


def test_method_name_collision_fails_loud():
    # DoX and Do_X both snake-case to do_x.
    fd = _file(
        "a.proto",
        "svc.v1",
        ["Req", "Resp"],
        [("S", [("DoX", ".svc.v1.Req", ".svc.v1.Resp"), ("Do_X", ".svc.v1.Req", ".svc.v1.Resp")])],
    )
    with pytest.raises(ValueError, match="method name collision"):
        facade_gen.generate(_desc_request([fd], ["a.proto"]))


def test_same_named_service_across_files_collides():
    common = ["Req", "Resp"]
    files = [
        _file("a.proto", "svc.v1", common, [("AuditService", [("Do", ".svc.v1.Req", ".svc.v1.Resp")])]),
        _file("b.proto", "svc.v1", [], [("AuditService", [("Do2", ".svc.v1.Req", ".svc.v1.Resp")])]),
    ]
    with pytest.raises(ValueError, match="service class collision"):
        facade_gen.generate(_desc_request(files, ["a.proto", "b.proto"]))


def test_missing_cross_package_type_fails_loud():
    # A method resolving into a package the contract never generated used to
    # raise a bare KeyError out of the type index (issue #410).
    fd = _file(
        "a.proto",
        "svc.v1",
        ["Req"],
        [("S", [("Do", ".svc.v1.Req", ".other.v1.Resp")])],
    )
    with pytest.raises(ValueError, match=r"no binding for type \.other\.v1\.Resp"):
        facade_gen.generate(_desc_request([fd], ["a.proto"]))


def test_cross_package_type_resolves_when_its_proto_is_present():
    files = [
        _file("jobs.proto", "other.v1", ["Resp"], []),
        _file("a.proto", "svc.v1", ["Req"], [("S", [("Do", ".svc.v1.Req", ".other.v1.Resp")])]),
    ]
    content = facade_gen.generate(_desc_request(files, ["a.proto"])).file[0].content

    assert "import jobs_pb2 as " in content
    assert ".Resp)" in content


def test_multiple_packages_emit_separate_files():
    files = [
        _file("a.proto", "one.v1", ["Req", "Resp"], [("AlphaService", [("Do", ".one.v1.Req", ".one.v1.Resp")])]),
        _file("b.proto", "two.v1", ["Req", "Resp"], [("BetaService", [("Do", ".two.v1.Req", ".two.v1.Resp")])]),
    ]
    response = facade_gen.generate(_desc_request(files, ["a.proto", "b.proto"]))

    names = sorted(f.name for f in response.file)
    assert names == ["one.py", "two.py"]
    one = next(f.content for f in response.file if f.name == "one.py")
    assert '"/one.v1.AlphaService/Do"' in one
    assert "two.v1" not in one  # packages did not bleed together


def test_explicit_module_rejected_when_packages_are_ambiguous():
    # module= names one entry point. Silently deriving a different name per
    # package instead breaks the caller's import; say so rather than renaming.
    files = [
        _file("a.proto", "saas.accounts.v1", ["Req", "Resp"],
              [("APIKeyService", [("L", ".saas.accounts.v1.Req", ".saas.accounts.v1.Resp")])]),
        _file("j.proto", "saas.jobs.v1", ["JReq", "JResp"],
              [("JobService", [("G", ".saas.jobs.v1.JReq", ".saas.jobs.v1.JResp")])]),
    ]
    with pytest.raises(ValueError, match="module=sdkclient names one entry point"):
        facade_gen.generate(_desc_request(files, ["a.proto", "j.proto"], "module=sdkclient"))


def test_services_filter_narrows_packages_so_module_is_honored():
    files = [
        _file("a.proto", "saas.accounts.v1", ["Req", "Resp"],
              [("APIKeyService", [("L", ".saas.accounts.v1.Req", ".saas.accounts.v1.Resp")])]),
        _file("j.proto", "saas.jobs.v1", ["JReq", "JResp"],
              [("JobService", [("G", ".saas.jobs.v1.JReq", ".saas.jobs.v1.JResp")])]),
    ]
    response = facade_gen.generate(
        _desc_request(files, ["a.proto", "j.proto"], "module=sdkclient,services=APIKeyService")
    )

    assert [f.name for f in response.file] == ["sdkclient.py"]
    assert "JobService" not in response.file[0].content


def test_main_reports_error_via_response(monkeypatch):
    fd = _file(
        "a.proto",
        "svc.v1",
        ["Req", "Resp"],
        [("S", [("DoX", ".svc.v1.Req", ".svc.v1.Resp"), ("Do_X", ".svc.v1.Req", ".svc.v1.Resp")])],
    )
    request = _desc_request([fd], ["a.proto"])
    written = {}

    monkeypatch.setattr(facade_gen.sys, "stdin", type("S", (), {"buffer": io.BytesIO(request.SerializeToString())}))
    monkeypatch.setattr(
        facade_gen.sys, "stdout", type("S", (), {"buffer": type("B", (), {"write": lambda _self, data: written.setdefault("data", data)})()})
    )
    facade_gen.main()

    response = plugin_pb2.CodeGeneratorResponse.FromString(written["data"])
    assert "method name collision" in response.error
    assert not response.file


# --- streaming shapes against an in-process grpcio server --------------------

SHAPES_PROTO = "shapes/v1/shapes.proto"


class ChannelGateway:
    """A gateway over a `grpc.Channel`: each seam is the channel multicallable of
    the same name (`unary` is `unary_unary`)."""

    def __init__(self, channel):
        self._channel = channel

    def _callable(self, kind, procedure, response_type):
        return getattr(self._channel, kind)(
            procedure,
            request_serializer=lambda message: message.SerializeToString(),
            response_deserializer=response_type.FromString,
        )

    def unary(self, procedure, request, response_type):
        return self._callable("unary_unary", procedure, response_type)(request, timeout=10)

    def unary_stream(self, procedure, request, response_type):
        return self._callable("unary_stream", procedure, response_type)(request, timeout=10)

    def stream_unary(self, procedure, requests, response_type):
        return self._callable("stream_unary", procedure, response_type)(requests, timeout=10)

    def stream_stream(self, procedure, requests, response_type):
        return self._callable("stream_stream", procedure, response_type)(requests, timeout=10)


def _shapes_facade(tmp_path: Path):
    root = tmp_path / "sdk"
    root.mkdir()
    subprocess.run(["protoc", "-I", str(FIXTURES), f"--python_out={root}", SHAPES_PROTO], check=True)
    for package in ("shapes", "shapes/v1"):
        (root / package / "__init__.py").touch()
    out = tmp_path / "shapes.pb"
    subprocess.run(
        ["protoc", "-I", str(FIXTURES), "--include_imports", f"--descriptor_set_out={out}", SHAPES_PROTO],
        check=True,
    )
    request = plugin_pb2.CodeGeneratorRequest(parameter="module=echoes")
    request.proto_file.extend(FileDescriptorSet.FromString(out.read_bytes()).file)
    request.file_to_generate.append(SHAPES_PROTO)
    facade = facade_gen.generate(request).file[0]
    (root / facade.name).write_text(facade.content)

    sys.path.insert(0, str(root))
    try:
        for mod in list(sys.modules):
            if mod == "echoes" or mod == "shapes" or mod.startswith("shapes."):
                del sys.modules[mod]
        return facade.content, importlib.import_module("echoes"), importlib.import_module("shapes.v1.shapes_pb2")
    finally:
        sys.path.remove(str(root))


def test_each_rpc_shape_calls_its_gateway_seam(tmp_path):
    content, _, _ = _shapes_facade(tmp_path)

    assert 'def echo(self, request):\n        return self._gateway.unary("/shapes.v1.EchoService/Echo", request,' in content
    assert 'def fan(self, request):\n        return self._gateway.unary_stream("/shapes.v1.EchoService/Fan", request,' in content
    assert 'def gather(self, requests):\n        return self._gateway.stream_unary("/shapes.v1.EchoService/Gather", requests,' in content
    assert 'def chat(self, requests):\n        return self._gateway.stream_stream("/shapes.v1.EchoService/Chat", requests,' in content
    assert "`unary_stream`, `stream_unary`, `stream_stream`" in content


def test_unary_only_facade_keeps_its_header(tmp_path):
    content = facade_gen.generate(_request(tmp_path, "module=accounts")).file[0].content
    assert "`unary(procedure, request, response_type)` owns the transport, bearer, and" in content
    assert "unary_stream" not in content


def test_every_rpc_shape_streams_through_grpc(tmp_path):
    grpc = pytest.importorskip("grpc")
    from concurrent import futures

    _, echoes, pb = _shapes_facade(tmp_path)
    Note = pb.Note
    seen_by_server: dict[str, list[str]] = {}

    def echo(request, _context):
        return Note(text=request.text.upper())

    def fan(request, _context):
        for index in range(request.count):
            yield Note(text=f"{request.text}-{index}", count=index)

    def gather(requests, _context):
        texts = [note.text for note in requests]
        seen_by_server["gather"] = texts
        return Note(text="+".join(texts), count=len(texts))

    def chat(requests, _context):
        received = seen_by_server.setdefault("chat", [])
        for note in requests:
            received.append(note.text)
            yield Note(text=f"re:{note.text}", count=len(received))

    def handler(kind, behaviour):
        return kind(behaviour, request_deserializer=Note.FromString, response_serializer=Note.SerializeToString)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    server.add_generic_rpc_handlers((
        grpc.method_handlers_generic_handler(
            "shapes.v1.EchoService",
            {
                "Echo": handler(grpc.unary_unary_rpc_method_handler, echo),
                "Fan": handler(grpc.unary_stream_rpc_method_handler, fan),
                "Gather": handler(grpc.stream_unary_rpc_method_handler, gather),
                "Chat": handler(grpc.stream_stream_rpc_method_handler, chat),
            },
        ),
    ))
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            client = echoes.echoes(ChannelGateway(channel)).echo()

            reply = client.echo(Note(text="hi"))
            assert isinstance(reply, Note) and reply.text == "HI"

            fanned = list(client.fan(Note(text="n", count=3)))
            assert [(n.text, n.count) for n in fanned] == [("n-0", 0), ("n-1", 1), ("n-2", 2)]
            assert all(isinstance(n, Note) for n in fanned)

            gathered = client.gather(iter([Note(text="a"), Note(text="b"), Note(text="c")]))
            assert (gathered.text, gathered.count) == ("a+b+c", 3)
            assert seen_by_server["gather"] == ["a", "b", "c"]

            chatted = list(client.chat(iter([Note(text="x"), Note(text="y")])))
            assert [(n.text, n.count) for n in chatted] == [("re:x", 1), ("re:y", 2)]
            assert seen_by_server["chat"] == ["x", "y"]
    finally:
        server.stop(grace=None)
