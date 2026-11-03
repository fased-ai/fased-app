import importlib.util
import hashlib
import json
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('staged_guest', pathlib.Path(__file__).with_name('staged-package-guest.py'))
guest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guest)

class DeliveryFixtureTest(unittest.TestCase):
    def package(self, root, key, version, packet_version):
        path = root / key
        path.mkdir()
        packet = {'version': packet_version, 'source': 'a' * 40, 'productVersion': version,
                  'release': {'sourceTree': 'b' * 40, 'runId': '123', 'runAttempt': '2', 'tagObject': ''}}
        identities = []
        for name in guest.ASSETS[:4]:
            raw = (key + ':' + name).encode()
            (path / name).write_bytes(raw)
            identities.append({'file': name, 'size': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()})
        packet['archive'], packet['installer'], packet['release']['provenance'], packet['entry'] = identities
        (path / guest.ASSETS[4]).write_text(json.dumps(packet))
        return path

    def test_delivery_roles_and_observed_failure_are_distinct(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            paths = {key: self.package(root, key, version, format) for key, version, format in
                     [('candidate', '0.1.6', 2), ('predecessor', '0.1.4', 2), ('legacy', '0.1.5', 1)]}
            guest.write_private(root / 'fixture.json', {k: str(v) for k, v in paths.items()})
            guest.write_private(root / 'selection.json', {'release': 'predecessor'})
            server = guest.Fixture(root)
            code, body = server.response('/repos/fased-ai/fased-app/releases/latest')
            self.assertEqual((code, body['tag_name']), (200, 'v0.1.4'))
            tags = []
            for version in ['0.1.6', '0.1.4', '0.1.5']:
                code, tag = server.response('/repos/fased-ai/fased-app/git/ref/tags/v' + version)
                tags.append(tag['object']['sha'])
                code, value = server.response('/repos/fased-ai/fased-app/git/tags/' + tag['object']['sha'])
                self.assertEqual(value['tag'], 'v' + version)
            self.assertEqual(len(set(tags)), 3)
            guest.write_private(root / 'selection.json', {'release': 'candidate', 'failArchiveOnce': True})
            request = '/fased-ai/fased-app/releases/download/v0.1.6/fased-app-linux-amd64.tar.gz'
            self.assertEqual(server.response(request)[0], 503)
            self.assertTrue(guest.document(root / 'selection.json')['archiveFailureObserved'])
            self.assertEqual(server.response(request)[0], 200)
            self.assertEqual(server.response('/unconfigured')[0], 404)

    def test_modified_or_linked_payload_is_not_a_package(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = self.package(pathlib.Path(temporary), 'candidate', '0.1.6', 2)
            guest.package(path)
            archive = path / guest.ASSETS[0]
            archive.write_bytes(b'altered')
            with self.assertRaisesRegex(ValueError, 'changed'):
                guest.package(path)
            archive.unlink()
            archive.symlink_to(path / guest.ASSETS[1])
            with self.assertRaisesRegex(ValueError, 'regular'):
                guest.package(path)

if __name__ == '__main__':
    unittest.main()
