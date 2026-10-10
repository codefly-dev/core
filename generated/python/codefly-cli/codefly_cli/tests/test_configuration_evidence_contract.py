import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from codefly.base.v0 import configuration_evidence_pb2 as evidence
from google.protobuf import json_format


class ConfigurationEvidenceContractTest(unittest.TestCase):
    def test_decision_rule_has_a_shared_enum_on_binary_and_json_transports(self):
        decision = evidence.ConfigurationDecision(
            group="app",
            module="host",
            rule=evidence.CONFIGURATION_RULE_WORKSPACE_KEY_REPLACES_MODULE_DEFAULT,
            final=True,
            selected=[evidence.ConfigurationOrigin(
                group="app", key="TOKEN", file="configurations/local/app.secret.ref.env"
            )],
            shadowed=[evidence.ConfigurationOrigin(
                group="app", key="TOKEN", file="../host/configurations/local/app.env"
            )],
        )
        restored = evidence.ConfigurationDecision.FromString(decision.SerializeToString())
        self.assertEqual(decision, restored)
        wire_json = json_format.MessageToDict(restored)
        self.assertEqual(
            "CONFIGURATION_RULE_WORKSPACE_KEY_REPLACES_MODULE_DEFAULT", wire_json["rule"]
        )
        self.assertEqual(decision, json_format.ParseDict(wire_json, evidence.ConfigurationDecision()))

    def test_document_and_profile_paths_survive_a_round_trip(self):
        origin = evidence.ConfigurationOrigin(
            group="policy", file="../host/configurations/staging/policy.yaml", document=True
        )
        self.assertEqual(origin, evidence.ConfigurationOrigin.FromString(origin.SerializeToString()))
        self.assertEqual("", origin.key)
        selection = evidence.ConfigurationProfileSelection(
            location="../host/configurations", candidates=["staging", "local"],
            layers=["../host/configurations/local", "../host/configurations/staging"], found=True,
        )
        self.assertEqual(selection, evidence.ConfigurationProfileSelection.FromString(selection.SerializeToString()))


if __name__ == "__main__":
    unittest.main()
