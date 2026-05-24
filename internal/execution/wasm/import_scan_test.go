package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScriptNeedsHeavyRuntime(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		heavy bool
	}{
		// --- should trigger Pyodide ---
		{"import scipy", "import scipy\n", true},
		{"import scipy.stats", "import scipy.stats\n", true},
		{"from scipy import optimize", "from scipy import optimize\n", true},
		{"from scipy.linalg import solve", "from scipy.linalg import solve\n", true},
		{"import pandas", "import pandas as pd\n", true},
		{"from pandas import DataFrame", "from pandas import DataFrame\n", true},
		{"import statsmodels", "import statsmodels.api as sm\n", true},
		{"import sklearn", "import sklearn\n", true},
		{"import matplotlib", "import matplotlib.pyplot as plt\n", true},
		{"import seaborn", "import seaborn as sns\n", true},
		{"multiline with scipy", "import numpy\nimport scipy\nimport math\n", true},
		// --- should NOT trigger ---
		{"pure numpy", "import numpy as np\n", false},
		{"pure sympy", "import sympy\n", false},
		{"stdlib only", "import math, json, sys\n", false},
		{"eval-python eval.py style", "def evaluation_function(r, a, p):\n    return r == a\n", false},
		{"word boundary: nopandas", "import nopandas\n", false},
		{"word boundary: pandasx", "import pandasx\n", false},
		{"comment line", "# import scipy\n", false},
		{"scipy in string", `x = "import scipy"` + "\n", false},
		{"indented from scipy", "    from scipy import stats\n", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScriptNeedsHeavyRuntime(tc.src)
			assert.Equal(t, tc.heavy, got, "ScriptNeedsHeavyRuntime(%q)", tc.src)
		})
	}
}
