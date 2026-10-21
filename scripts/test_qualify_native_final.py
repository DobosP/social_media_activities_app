"""Source-only authored negative controls; Root runs after source admission."""
import importlib.util,json,lzma,os,tempfile,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('native21_final',Path(__file__).with_name('qualify-native-final.py'))
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
# Task-owned TMPDIR when set; otherwise the platform temporary directory (TI-6).
def scratch_dir():return Path(os.environ.get('TMPDIR') or tempfile.gettempdir())
class FinalNativeBoundaryTests(unittest.TestCase):
    def test_affected_driver_has_exact_inverse_and_distinct_full_status(self):
        native=Path(__file__).with_name('qualify-native.sh').read_bytes()
        with tempfile.TemporaryDirectory(dir=scratch_dir()) as temporary:
            source=Path(temporary).resolve()/"exported source with ' quote";source.mkdir()
            derived,proof=module.affected_driver(native,source)
            self.assertEqual(proof['canonical_sha256'],module.NATIVE_SHA)
            self.assertTrue(proof['exact_inverse']);self.assertEqual(len(proof['splices']),2)
            inverse=derived.decode()
            for splice in reversed(proof['splices']):
                self.assertEqual(inverse.count(splice['after']),1)
                inverse=inverse.replace(splice['after'],splice['before'],1)
            self.assertEqual(inverse.encode(),native)
            self.assertEqual(proof['inverse_sha256'],module.NATIVE_SHA)
            self.assertEqual(derived.count(b'for native_package in catalog web; do\n'),1)
            self.assertIn(('native_root='+module.shlex_quote(str(source))+'\n').encode(),derived)
            with self.assertRaises(RuntimeError):module.affected_driver(native+b'# drift\n',source)
        self.assertEqual(module.qualification_mode(False),(module.PACKAGES,'PASS_FULL_NATIVE21'))
        self.assertEqual(module.qualification_mode(True),(['catalog','web'],'PASS_AFFECTED_CATALOG_WEB_ONLY'))
        self.assertNotEqual(module.qualification_mode(False)[1],module.qualification_mode(True)[1])
        for invalid in [None,1,'catalog','web']:
            with self.assertRaises(RuntimeError):module.qualification_mode(invalid)
    def test_fixture_wal_entry_retains_durability_and_refuses_ambiguous_anchor(self):
        original = 'fsync = on\nfull_page_writes = on\nsynchronous_commit = on\nport = 5432\nPG_CONF\nstarted=1\n'
        added = 'max_wal_size = 128MB\nmin_wal_size = 32MB\ncheckpoint_timeout = 30s\n'
        result = module.fixture_wal_entry(original)
        self.assertEqual(result.replace(added, '', 1), original)
        self.assertEqual(result.count(added), 1)
        self.assertTrue(result.index(added) < result.index('PG_CONF'))
        for invalid in [original.replace('port = 5432', 'port = 55434'), original + 'port = 5432\nPG_CONF\n']:
            with self.assertRaises(RuntimeError): module.fixture_wal_entry(invalid)
    def test_wrapper_records_actual_package_exit_and_rejects_filtering(self):
        import uuid
        from unittest.mock import patch
        with tempfile.TemporaryDirectory(dir=scratch_dir()) as temporary:
            out=Path(temporary).resolve();source=out/'source';scratch=out/'runtime'
            plan={'source':str(source),'scratch':str(scratch),'out':str(out),'codec':module.CODEC_IMAGE,'owner':str(uuid.uuid4()),'network':'native21-owned-network','package_name':'native21-owned-package','docker':'/usr/bin/docker'}
            plan_file=out/'plan.json';plan_file.write_text(json.dumps(plan))
            prefix=['run','--rm','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--user',str(os.getuid())+':'+str(os.getgid()),'--network',plan['network'],'-e','TMPDIR=/scratch/test-tmp','-e','GOMAXPROCS=2','-v',str(source)+':/src:ro','-v',str(scratch)+':/scratch','-w','/src/services/server/cmd/social-server',module.CODEC_IMAGE,'/scratch/tests/configuration.test','-test.v','-test.timeout=10m']
            # Subprocesses are isolated stubs: no Docker invocation occurs.
            for code in (0,7):
                with patch.object(module.subprocess,'Popen') as popen, patch.object(module.subprocess,'run') as inspect:
                    popen.return_value.wait.return_value=code;inspect.return_value.returncode=1
                    self.assertEqual(module.docker_wrapper(plan_file,prefix.copy()),code)
                receipt=json.loads((out/'package-configuration.json').read_text())
                self.assertEqual(receipt['exit'],code)
                self.assertEqual(receipt['argv'][-3:],['/scratch/tests/configuration.test','-test.v','-test.timeout=10m'])
            with patch.object(module.subprocess,'Popen') as popen:
                with self.assertRaises(RuntimeError):module.docker_wrapper(plan_file,prefix+['-test.run=Filtered'])
                popen.assert_not_called()
    def test_fixture_host_admission_requires_owned_empty_private_network(self):
        import copy
        network={'Internal':True,'Labels':{module.OWNER_LABEL:'owner'},'Containers':{},'IPAM':{'Config':[{'Subnet':'172.31.240.0/24'}]}}
        self.assertEqual(module.fixture_host_rule(network,'owner'),'host all app 172.31.240.0/24 trust')
        invalid=[]
        for key,value in [('Internal',False),('Labels',{module.OWNER_LABEL:'other'}),('Containers',{'foreign':{}}),('IPAM',{'Config':[]}),('IPAM',{'Config':[{'Subnet':'8.8.8.0/24'}]}),('IPAM',{'Config':[{'Subnet':'127.0.0.0/8'}]}),('IPAM',{'Config':[{'Subnet':'fd00::/64'}]}),('IPAM',{'Config':[{'Subnet':'172.31.240.1/24'}]}),('IPAM',{'Config':[{'Subnet':'172.31.240.0/24'},{'Subnet':'172.31.241.0/24'}]})]:
            changed=copy.deepcopy(network);changed[key]=value;invalid.append(changed)
        for changed in invalid:
            with self.assertRaises((RuntimeError,ValueError)):module.fixture_host_rule(changed,'owner')
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
        scratch=scratch_dir();self.assertTrue(scratch.is_absolute())
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
    def test_scratch_falls_back_to_platform_temp_without_tmpdir(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory(dir=scratch_dir()) as owned:
            with patch.dict(os.environ,{'TMPDIR':owned}):self.assertEqual(scratch_dir(),Path(owned))
        with patch.dict(os.environ):
            os.environ.pop('TMPDIR',None);fallback=scratch_dir()
            self.assertEqual(fallback,Path(tempfile.gettempdir()));self.assertTrue(fallback.is_absolute() and fallback.is_dir())
            with patch.dict(os.environ,{'TMPDIR':''}):self.assertEqual(scratch_dir(),fallback)
if __name__=='__main__':unittest.main()
