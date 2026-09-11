import hashlib
import unittest
from concurrent import futures

import grpc

from codefly.services.agent.v0 import agent_pb2, agent_pb2_grpc, inputs_pb2


class EffectiveInputsContractTest(unittest.TestCase):
    def test_published_runtime_requirements_api(self):
        self.assertEqual(agent_pb2.Runtime.NIX, 8)
        self.assertEqual(agent_pb2.Runtime.RUST, 9)
        self.assertEqual(agent_pb2.Runtime.CARGO, 10)
        message = agent_pb2.AgentInformation(runtime_requirements=[
            agent_pb2.Runtime(type=agent_pb2.Runtime.GO, version="1.27")
        ])
        # Serialized by the SDK on the base branch, before input discovery.
        published_wire = bytes.fromhex("0a0808011204312e3237")
        self.assertEqual(message.SerializeToString(), published_wire)
        self.assertEqual(
            agent_pb2.AgentInformation.FromString(published_wire), message
        )

    def test_v1_identity_matches_go(self):
        task = inputs_pb2.TaskInputs(
            task=inputs_pb2.TaskKey(phase=inputs_pb2.TASK_PHASE_ARTIFACT_BUILD),
            complete=True,
        )
        for kind, name, content in [
            (inputs_pb2.EFFECTIVE_INPUT_KIND_TOOLCHAIN, "toolchain", b"resolved-toolchain"),
            (inputs_pb2.EFFECTIVE_INPUT_KIND_PLUGIN, "agent", b"resolved-agent"),
        ]:
            task.inputs.add(
                kind=kind,
                owner="frontend",
                name=name,
                identity=inputs_pb2.EffectiveIdentity(
                    kind=inputs_pb2.EFFECTIVE_IDENTITY_KIND_SHA256,
                    digest=hashlib.sha256(content).hexdigest(),
                ),
            )
        digest = hashlib.sha256(
            b"codefly.effective-inputs/v1\0"
            + task.SerializeToString(deterministic=True)
        ).hexdigest()
        self.assertEqual(
            digest, "cf974b884f3095d736ba2b81da97774a2d25d19999b2ad039592d6f32cd873a2"
        )

    def test_discovery_rpc(self):
        class Agent(agent_pb2_grpc.AgentServicer):
            def GetEffectiveInputs(self, request, context):
                return inputs_pb2.GetEffectiveInputsResponse(
                    schema_version=request.schema_version,
                    snapshot=request.snapshot,
                )

        with futures.ThreadPoolExecutor(max_workers=1) as executor:
            server = grpc.server(executor)
            agent_pb2_grpc.add_AgentServicer_to_server(Agent(), server)
            port = server.add_insecure_port("127.0.0.1:0")
            server.start()
            try:
                with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                    response = agent_pb2_grpc.AgentStub(channel).GetEffectiveInputs(
                        inputs_pb2.GetEffectiveInputsRequest(
                            schema_version=1, snapshot="candidate"
                        ),
                        timeout=5,
                    )
                self.assertEqual(response.schema_version, 1)
                self.assertEqual(response.snapshot, "candidate")
            finally:
                server.stop(0).wait()


if __name__ == "__main__":
    unittest.main()
