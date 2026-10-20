"""Source-only authored negative controls; Root runs after source admission."""
import importlib.util,json,lzma,os,tempfile,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('native21_final',Path(__file__).with_name('qualify-native-final.py'))
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
class FinalNativeBoundaryTests(unittest.TestCase):
    def test_exact_xz_caps_single_crc64_stream(self):
        raw=b'fixture';compressed=lzma.compress(raw,check=lzma.CHECK_CRC64)
        self.assertEqual(module.strict_xz_receipt.__defaults__,(64<<20,128<<20,64<<20))
        self.assertEqual(module.strict_xz_receipt(compressed)['expanded_tar_bytes'],len(raw))
        for invalid in [compressed[:-1],compressed+b'x',compressed+compressed,lzma.compress(raw,check=lzma.CHECK_CRC32)]:
            with self.assertRaises((RuntimeError,lzma.LZMAError)):module.strict_xz_receipt(invalid)
    def test_daemon_failure_is_not_object_absence(self):
        name='native21-generated-db'
        self.assertIsNone(module.inspected_object(1,b'[]',('Error: No such object: '+name).encode(),name))
        for error in [b'Cannot connect to the Docker daemon',b'permission denied',b'connection refused',b'Error: No such object: another-owner']:
            with self.assertRaises(RuntimeError):module.inspected_object(1,b'[]',error,name)
        with self.assertRaises(RuntimeError):module.inspected_object(0,b'[]',b'',name)
        value={'Name':'/'+name,'Config':{'Labels':{module.OWNER_LABEL:'owner'}}}
        self.assertEqual(module.inspected_object(0,json.dumps([value]).encode(),b'',name),value)
    def test_full_git_path_blob_and_mode_fail_closed(self):
        import hashlib
        scratch=Path(os.environ['TMPDIR']);self.assertTrue(scratch.is_absolute())
        with tempfile.TemporaryDirectory(dir=scratch) as temporary:
            root=Path(temporary);file=root/'fixture';raw=b'one\n';file.write_bytes(raw);file.chmod(0o644)
            oid=hashlib.sha1(b'blob 4\0'+raw).hexdigest();expected={'fixture':('100644',oid)}
            self.assertEqual(module.tree_rows(root,expected)[0]['git_blob'],oid)
            file.chmod(0o755)
            with self.assertRaises(RuntimeError):module.tree_rows(root,expected)
            file.chmod(0o644);file.write_bytes(b'two\n')
            with self.assertRaises(RuntimeError):module.tree_rows(root,expected)
            file.write_bytes(raw);(root/'extra').write_bytes(b'extra')
            with self.assertRaises(RuntimeError):module.tree_rows(root,expected)
if __name__=='__main__':unittest.main()
