"""The generated value carries acquisition evidence through the existing wire."""
import unittest
from google.protobuf import json_format
from codefly.base.v0 import configuration_pb2 as configuration
from codefly.base.v0 import source_pb2 as source


class ConfigurationArtifactContract(unittest.TestCase):
    def test_declaration_and_delivery_fields(self):
        declaration = configuration.ProfileModuleArtifactConfigurations()
        json_format.Parse('{"schemaVersion":1,"bindings":[{"configuration":"runtime","key":"INSTALL","source":{"module":"documents","path":"public.json"}}]}', declaration)
        self.assertEqual(declaration.schema_version, 1)
        value = configuration.ConfigurationValue(key="INSTALL", value="{}")
        value.resolved_module_artifact.reference.CopyFrom(declaration.bindings[0].source)
        value.resolved_module_artifact.artifact_identity.CopyFrom(source.SourceIdentity(
            algorithm=source.SOURCE_IDENTITY_ALGORITHM_SHA256,
            digest="44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a", size_bytes=2))
        clone = configuration.ConfigurationValue.FromString(value.SerializeToString())
        self.assertEqual(clone, value)
        self.assertEqual(value.DESCRIPTOR.fields_by_name["module_artifact"].number, 5)
        self.assertEqual(value.DESCRIPTOR.fields_by_name["resolved_module_artifact"].number, 6)
        self.assertFalse(value.HasField("module_artifact"))
