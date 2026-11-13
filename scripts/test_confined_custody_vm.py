#!/usr/bin/env python3
"""Boundary regressions for the disposable Local acceptance driver."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parent / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


host = load('confined_host', 'check-confined-custody-vm.py')
guest = load('confined_guest', 'confined-custody-fixture.py')


class FixtureBoundaries(unittest.TestCase):
    def test_source_identity_cannot_be_absent_or_different(self):
        sha = 'a' * 40
        self.assertFalse(host.matching_source({}, {}, {}))
        self.assertFalse(host.matching_source({'declaredSourceCommit': sha}, {'declaredSourceCommit': sha}, {}))
        self.assertFalse(host.matching_source(*[{'declaredSourceCommit': 'A' * 40}] * 3))
        self.assertTrue(host.matching_source(*[{'declaredSourceCommit': sha}] * 3))

    def test_parent_traversal_refused_before_fixture_access(self):
        with patch.dict(os.environ, {'FASED_DISPOSABLE_VM_ACCEPTANCE': '1'}):
            with self.assertRaisesRegex(RuntimeError, 'parent traversal'):
                host.preflight(host.EVIDENCE / 'candidate' / '..' / '..' / 'foreign', 'a' * 64)

    def test_archive_rejects_symlinks_and_aggregate_size(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'file').write_text('fixture')
            (base / 'link').symlink_to(base / 'file')
            with self.assertRaisesRegex(RuntimeError, 'symlink'):
                host.safe_files([base])
            (base / 'link').unlink()
            for n in range(3):
                with (base / str(n)).open('wb') as stream:
                    stream.truncate(90 << 20)
            with self.assertRaisesRegex(RuntimeError, 'size limit'):
                host.safe_files([base])

    def test_completed_or_wrong_signer_is_not_a_held_update(self):
        state = {'initialOperation': '0' * 32}
        trust = {'targetSha256': 'a' * 64, 'targetSignerSha256': 'b' * 64}
        journal = {'operationId': '1' * 32, 'phase': 'ready',
                   'installation': {'phase': 'active', 'digest': 'c' * 64,
                                    'binding': {'targetCore': 'a' * 64, 'targetSigner': 'b' * 64}}}
        self.assertTrue(guest.matching_held_update(journal, state, trust))
        journal['installation']['phase'] = 'completed'
        self.assertFalse(guest.matching_held_update(journal, state, trust))
        journal['installation']['phase'] = 'active'
        journal['installation']['binding']['targetSigner'] = 'd' * 64
        self.assertFalse(guest.matching_held_update(journal, state, trust))

    def test_archive_rejects_too_many_entries(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            for n in range(257):
                (base / str(n)).write_text('x')
            with self.assertRaisesRegex(RuntimeError, 'entry limit'):
                host.safe_files([base])

    def test_cut_requires_stopped_exact_signer(self):
        guest.require_stopped_signer('State:\tT (stopped)\nUid:\t1102\t1102\t1102\t1102\n', 1102)
        with self.assertRaises(BlockingIOError):
            guest.require_stopped_signer('State:\tS (sleeping)\nUid:\t1102\t1102\t1102\t1102\n', 1102)
        with self.assertRaisesRegex(RuntimeError, 'identity differs'):
            guest.require_stopped_signer('State:\tT (stopped)\nUid:\t1100\t1100\t1100\t1100\n', 1102)

    def test_failure_detail_excludes_fixture_credentials(self):
        output = guest.safe_fixture_error('fased-install: token=private-value ' + guest.PASSWORD + '\nfased-app: source image refused')
        self.assertNotIn('private-value', output)
        self.assertNotIn(guest.PASSWORD, output)
        self.assertEqual('', output)
        self.assertEqual('protected enrollment source validation refused', guest.safe_fixture_error('fased-install: enrollment source payload validation: state refused'))
        good = b'fixture: fixture command failed: fased-app exit 1; protected enrollment source validation refused\n'
        self.assertEqual('fixture command failed: fased-app exit 1; protected enrollment source validation refused', host.fixture_failure_detail(good))
        self.assertEqual('', host.fixture_failure_detail(b'fixture: fixture command failed: fased-app exit 1; token=private\n'))
        self.assertEqual('fixture API /api/v1/auth/claim HTTP 409', host.fixture_failure_detail(b'fixture: fixture API /api/v1/auth/claim HTTP 409\n'))
        self.assertEqual('', host.fixture_failure_detail(b'fixture: fixture API /api/v1/auth/claim HTTP 409 token=private\n'))

    def test_resume_requires_original_receipt_and_pinned_identities(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            work = base / 'vm-owned'
            work.mkdir(mode=0o700)
            for name in ('disk.qcow2', 'seed.iso', 'owner-key'):
                (work / name).write_text('fixture')
            (work / 'owner-key').chmod(0o600)
            (work / 'owner-key.pub').write_text('ssh-ed25519 owner-public\n')
            (work / 'host-key.pub').write_text('ssh-ed25519 host-public\n')
            (work / 'known_hosts').write_text('[127.0.0.1]:34163 ssh-ed25519 host-public\n')
            witness_dir = work / 'offline-inspection'
            witness_dir.mkdir()
            witness = witness_dir / 'partition.raw'
            witness.write_text('original snapshot')
            witness.chmod(0o600)
            image = base / 'ubuntu.img'
            chain = [{'format': 'qcow2', 'virtual-size': 12 << 30, 'full-backing-filename': str(image)},
                     {'format': 'qcow2', 'filename': str(image)}]
            def output(args):
                if args[0] == 'ssh-keygen':
                    return SimpleNamespace(stdout=b'ssh-ed25519 owner-public\n')
                if args[0] == 'debugfs':
                    value = 'ssh-ed25519 host-public\n' if 'ssh_host' in args[2] else 'd' * 32 + '\n'
                    return SimpleNamespace(stdout=value.encode())
                return SimpleNamespace(stdout=json.dumps(chain).encode())
            receipt = {'result': 'FAIL', 'fixtureOnly': True, 'sourceReceiptSha256': 'a' * 64,
                       'helperSha256': 'b' * 64, 'mining': False, 'liveFeatures': False,
                       'automaticUpdates': False,
                       'error': "fixture confined-custody-fixture.py setup --port 50943' failed"}
            result = base / 'local-result.json'
            result.write_text(json.dumps(receipt))
            with patch.object(host, 'IMAGE', image), patch.object(host, 'command', side_effect=output):
                self.assertEqual((34163, 50943, 'd' * 32), host.resume_inputs(work, result, 'a' * 64, 'b' * 64))
                chain[0]['full-backing-filename'] = '/unexpected/image'
                with self.assertRaisesRegex(RuntimeError, 'backing chain differs'):
                    host.resume_inputs(work, result, 'a' * 64, 'b' * 64)
                chain[0]['full-backing-filename'] = str(image)
                receipt['helperSha256'] = 'c' * 64
                result.write_text(json.dumps(receipt))
                with self.assertRaisesRegex(RuntimeError, 'receipt required'):
                    host.resume_inputs(work, result, 'a' * 64, 'b' * 64)
                receipt['helperSha256'] = 'b' * 64
                result.write_text(json.dumps(receipt))
                (work / 'known_hosts').write_text('[127.0.0.1]:34163 ssh-ed25519 other-host\n')
                with self.assertRaisesRegex(RuntimeError, 'host identity differs'):
                    host.resume_inputs(work, result, 'a' * 64, 'b' * 64)

    def test_resume_claimed_preserves_existing_enrollment(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            home, work, trust = base / 'home', base / 'work', base / 'trust'
            work.mkdir()
            trust.mkdir()
            (work / 'selection').write_text('base\n')
            binary = home / '.local/share/fased-app/fased-app'
            binary.parent.mkdir(parents=True)
            binary.write_text('original core')
            (trust / 'fixture.json').write_text(json.dumps({'sourceSha256': guest.digest(binary)}))
            manifest = {'phase': 'ready', 'location': 'local', 'listen': '127.0.0.1:50943',
                        'data': str(home / '.config/fased-app')}
            (binary.parent / 'install.json').write_text(json.dumps(manifest))
            with patch.multiple(guest, HOME=home, WORK=work, TRUST=trust, ROOT=base / 'custody'), \
                 patch.object(guest, 'api', return_value={'claimed': True}), \
                 patch.object(guest, 'login', return_value={'sessionToken': 'fixture'}), \
                 patch.object(guest, 'complete_setup', return_value='continued') as complete:
                self.assertEqual('continued', guest.resume_claimed(50943))
                complete.assert_called_once_with(50943, {'sessionToken': 'fixture'})
                (work / 'state.json').write_text('{}')
                with self.assertRaisesRegex(RuntimeError, 'pre-enrollment fixture required'):
                    guest.resume_claimed(50943)


if __name__ == '__main__':
    unittest.main()
