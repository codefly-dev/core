import unittest

from codefly.services.runtime.v0 import runtime_pb2


class RuntimeImageContractTest(unittest.TestCase):
    def test_init_image_wire_matches_go(self):
        for image in [
            "",
            "registry.example.test:5000/team/service:dev-123",
            "registry.example.test/team/service@sha256:" + "a" * 64,
            # The wire carries input; the consuming agent owns refusals.
            "service:latest",
        ]:
            with self.subTest(image=image):
                request = runtime_pb2.InitRequest(runtime_image=image)
                payload = image.encode("utf-8")
                expected = b"\x52" + bytes([len(payload)]) + payload if image else b""
                wire = request.SerializeToString()
                self.assertEqual(wire, expected)
                restored = runtime_pb2.InitRequest.FromString(wire)
                self.assertEqual(restored.runtime_image, image)
                self.assertEqual(restored, request)
