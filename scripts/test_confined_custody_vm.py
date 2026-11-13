#!/usr/bin/env python3
"""Boundary regressions for the disposable Local acceptance driver."""
import importlib.util
import os
from pathlib import Path
import tempfile
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


if __name__ == '__main__':
    unittest.main()
