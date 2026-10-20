"""Pure source derivation checks; never evaluates JavaScript or starts a browser."""
from pathlib import Path
import importlib.util,unittest

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('graph_csp_derivation',HERE/'derive_graph_csp.py')
derivation=importlib.util.module_from_spec(spec);spec.loader.exec_module(derivation)

class GraphCSPDerivationTest(unittest.TestCase):
 def test_exact_two_edits_preserve_all_other_bytes_and_notice(self):
  repo=HERE.parents[1];source=(repo/derivation.SOURCE_PATH).read_bytes();js,css,proof=derivation.derive(source)
  self.assertEqual(len(proof['edits']),2);self.assertTrue(js.startswith(derivation.SOURCE_NOTICE));self.assertNotIn(b'document.createElement("style")',js)
  previous=0;cursor=0
  for edit in proof['edits']:
   unchanged=source[previous:edit['start']];self.assertEqual(js[cursor:cursor+len(unchanged)],unchanged);cursor+=len(unchanged)
   self.assertEqual(source[edit['end']:edit['end']+1],b';');previous=edit['end']
  self.assertEqual(js[cursor:],source[previous:]);self.assertEqual(derivation.derive(source),(js,css,proof))
  self.assertIn(b'.graph-info-msg',css);self.assertIn(b'.scene-nav-info',css);self.assertEqual([e['css_csp_sha256'] for e in proof['edits']],derivation.CSS_CSP_SHA256)
 def test_changed_original_refuses_before_any_output(self):
  repo=HERE.parents[1];source=(repo/derivation.SOURCE_PATH).read_bytes()
  for changed in [source+b'\n',b'X'+source[1:]]:
   with self.assertRaises(ValueError):derivation.derive(changed)

if __name__=='__main__':unittest.main()
