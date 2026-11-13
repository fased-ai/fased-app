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


if __name__ == '__main__':
    unittest.main()
