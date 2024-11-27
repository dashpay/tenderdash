package llmq

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"testing"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
)

const defaultSeedSource = 999

func Test100Members(t *testing.T) {
	testCases := []struct {
		name      string
		n         int
		threshold int
		omit      int
	}{
		{
			name:      "test 100 members at threshold",
			n:         100,
			threshold: 67,
			omit:      33,
		},
		{
			name:      "test 100 members over threshold",
			n:         100,
			threshold: 67,
			omit:      rand.Intn(34),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			llmq, err := Generate(crypto.RandProTxHashes(tc.n), WithThreshold(tc.threshold))
			require.NoError(t, err)
			proTxHashesBytes := make([][]byte, tc.n)
			for i, proTxHash := range llmq.ProTxHashes {
				proTxHashesBytes[i] = proTxHash
			}
			signID := crypto.CRandBytes(32)
			signatures := make([][]byte, tc.n)
			for i, privKey := range llmq.PrivKeyShares {
				signatures[i], err = privKey.SignDigest(signID)
				require.NoError(t, err)
			}
			offset := 0
			if tc.omit > 0 {
				offset = rand.Intn(tc.omit)
			}
			check := tc.n - tc.omit
			require.True(t, check > tc.threshold-1)
			sig, err := bls12381.RecoverThresholdSignatureFromShares(
				signatures[offset:check+offset], proTxHashesBytes[offset:check+offset],
			)
			require.NoError(t, err)
			verified := llmq.ThresholdPubKey.VerifySignatureDigest(signID, sig)
			require.True(t, verified, "offset %d check %d", offset, check)
		})
	}
}

func TestLLMQFailed(t *testing.T) {
	signHash := mustHashFromString("b6d8ee31bbd375dfd55d5fb4b02cfccc68709e64f4c5ffcd3895ceb46540311d")
	llmqData := llmqTestDataGetter(4, 4, signHash)()
	testCases := []struct {
		i, j    int
		wantErr string
	}{
		{
			i: 0, j: 0,
			wantErr: "At least 2 shares required",
		},
		{
			i: 0, j: 2,
			wantErr: "At least 2 shares required",
		},
		{
			i: 2, j: 0,
			wantErr: "Numbers of shares and ids must be equal",
		},
		{
			i: 2, j: 4,
			wantErr: "Numbers of shares and ids must be equal",
		},
		{
			i: 4, j: 2,
			wantErr: "Numbers of shares and ids must be equal",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-cadse #%d", i), func(t *testing.T) {
			_, err := bls.ThresholdSignatureRecover(llmqData.sigShares[0:tc.i], llmqData.ids[0:tc.j])
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLLMQ(t *testing.T) {
	quorumHash := mustHashFromString("75899cad8bd52eb1105efa2204ed139bbdb4bb9ddcde4c090b579b3ab4b98fb0")
	signID := mustHashFromString("0000000000000000000000000000000000000000000000000000000000000001")
	msgHash := mustHashFromString("0000000000000000000000000000000000000000000000000000000000000002")
	signHash := mustHashFromString("b6d8ee31bbd375dfd55d5fb4b02cfccc68709e64f4c5ffcd3895ceb46540311d")
	require.Equal(t, signHash, bls.BuildSignHash(100, quorumHash, signID, msgHash))
	testCases := []struct {
		llmqDataGetter    func() llmqTestCaseData
		recoveryTestCases []recoveryTestCase
		rounds            int
	}{
		{
			llmqDataGetter: staticTestCase,
			rounds:         10,
			recoveryTestCases: []recoveryTestCase{
				{j: 3, want: false},
				{j: 5, want: false},
				{j: 6, want: true},
				{j: 10, want: true},
			},
		},
		{
			llmqDataGetter: llmqTestDataGetter(2, 2, signHash),
			rounds:         1,
			recoveryTestCases: []recoveryTestCase{
				{j: 0, want: false},
				{j: 1, want: false},
				{j: 2, want: true},
			},
		},
		{
			llmqDataGetter: llmqTestDataGetter(3, 2, signHash),
			rounds:         2,
			recoveryTestCases: []recoveryTestCase{
				{j: 1, want: false},
				{j: 2, want: true},
			},
		},
		{
			llmqDataGetter: llmqTestDataGetter(4, 3, signHash),
			rounds:         5,
			recoveryTestCases: []recoveryTestCase{
				{j: 1, want: false},
				{j: 2, want: false},
				{j: 3, want: true},
				{j: 4, want: true},
			},
		},
		{
			llmqDataGetter: llmqTestDataGetter(10, 6, signHash),
			rounds:         10,
			recoveryTestCases: []recoveryTestCase{
				{j: 1, want: false},
				{j: 5, want: false},
				{j: 6, want: true},
				{j: 10, want: true},
			},
		},
		{
			llmqDataGetter: llmqTestDataGetter(50, 30, signHash),
			rounds:         3,
			recoveryTestCases: []recoveryTestCase{
				{j: 1, want: false},
				{j: 3, want: false},
				{j: 29, want: false},
				{j: 30, want: true},
				{j: 40, want: true},
				{j: 50, want: true},
			},
		},
		{
			llmqDataGetter: llmqTestDataGetter(200, 100, signHash),
			rounds:         1,
			recoveryTestCases: []recoveryTestCase{
				{j: 50, want: false},
				{j: 70, want: false},
				{j: 99, want: false},
				{j: 100, want: true},
				{j: 200, want: true},
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			t.Parallel()
			llmqData := tc.llmqDataGetter()
			tc := tc

			for round := 0; round < tc.rounds; round++ {
				// For the given rounds shuffleSigsAndIDs the sigShares/ids each round to try various combinations
				shuffleSigsAndIDs(llmqData.sigShares, llmqData.ids)
				for _, r := range tc.recoveryTestCases {
					sigShares := llmqData.sigShares[r.i:r.j]
					ids := llmqData.ids[r.i:r.j]
					// Try recovery
					thresholdSig, _ := bls.ThresholdSignatureRecover(sigShares, ids)
					if !r.want {
						if thresholdSig != nil {
							require.False(t, thresholdSig.EqualTo(llmqData.wantThresholdSig))
						}
						continue
					}
					equalSigs(t, llmqData.wantThresholdSig, thresholdSig)
					// Try verification
					isVerified := bls.ThresholdVerify(llmqData.pkThreshold, signHash, thresholdSig)
					require.Equal(t, r.want, isVerified, "verify result mismatch - expected: %t, actual: %t", r.want, isVerified)
				}
			}
		})
	}
}

func equalSigs(t *testing.T, expected, actual *bls.G2Element) {
	require.True(t,
		expected.EqualTo(actual),
		"signature mismatch - expected: %x, actual: %x",
		expected.Serialize(),
		actual.Serialize(),
	)
}

type recoveryTestCase struct {
	i    int
	j    int
	want bool
}

type llmqTestCaseData struct {
	n, m             int
	proTxHashes      []crypto.ProTxHash
	ids              []bls.Hash
	sigShares        []*bls.G2Element
	signHash         bls.Hash
	pkThreshold      *bls.G1Element
	wantThresholdSig *bls.G2Element
}

func mustGenerateLLMQData(proTxHashes []crypto.ProTxHash, m int, _sigHash bls.Hash) *Data {
	llmqData, err := Generate(
		proTxHashes,
		WithThreshold(m),
		WithSeed(defaultSeedSource),
	)
	if err != nil {
		panic(err)
	}
	return llmqData
}

func llmqTestDataGetter(n, m int, sigHash bls.Hash) func() llmqTestCaseData {
	return func() llmqTestCaseData {
		llmqData := mustGenerateLLMQData(crypto.RandProTxHashes(n), m, sigHash)
		signs := mustThresholdSigns(llmqData.PrivKeys, sigHash)
		thresholdSig, blsSigShares := mustSignShares(llmqData.ProTxHashes, signs)
		sigShares := make([]*bls.G2Element, len(blsSigShares))
		for i, sigShare := range blsSigShares {
			sigShares[i] = mustG2ElementFromBytes(sigShare)
		}
		return llmqTestCaseData{
			n:                len(llmqData.ProTxHashes),
			m:                llmqData.Threshold,
			ids:              proTxHashes2BLSHashes(llmqData.ProTxHashes),
			proTxHashes:      llmqData.ProTxHashes,
			signHash:         sigHash,
			wantThresholdSig: mustG2ElementFromBytes(thresholdSig),
			sigShares:        sigShares,
			pkThreshold:      mustG1ElementFromBytes(llmqData.ThresholdPubKey.Bytes()),
		}
	}
}

func staticTestCase() llmqTestCaseData {
	proTxHashes := []crypto.ProTxHash{
		proTxHashFromHexString("E17048CB4ACB8DD21B5FE5A64E61972FC8E557763B50ABDA91A7753DF39A3C0A"),
		proTxHashFromHexString("BFDF4B1E6174CB60BFBCA0E88D07E19E9FAE76AE7C971CD7B49C7D719D967C32"),
		proTxHashFromHexString("DBBF8DCDF5EB812684DA473F9ADEF4B3CA4876C47443C435B6F2E6130E47E750"),
		proTxHashFromHexString("FD78940CB06B17EA16C7BD87FA983F1C15F712BBF8704481964A256BA7AF3960"),
		proTxHashFromHexString("CADFA86755BBC0FC39CA443E6181A4F5149B3CAAFE65F40C374F9439B3C58066"),
		proTxHashFromHexString("D6FE10371DA6F140D921E741DEBE0398A60651EC5196B501F517FE1118CCED91"),
		proTxHashFromHexString("F8C05A35E2B497FC1B34FDC89F45C407246737A26ED7916D972B14E9DF0F919F"),
		proTxHashFromHexString("5475E54B6E140A238890F0F661EDC4B1FCB2EFEB6E3EF8902D12D21864609EA0"),
		proTxHashFromHexString("42311D9B1E79EDC5F26321D8116FC4A0476E1794109DAAAEAB821B46ED129DE7"),
		proTxHashFromHexString("8B0DF1D740B2864A8F9F02418684079A01D376BCB8A1C3DE0AAC7054D44DD9FD"),
	}
	sigShares := []*bls.G2Element{
		mustSigFromString("872C1F1403607A9F3D362D7C9C06EE4B077DA59E16407C9726424100E503687B070D21975251DF18CD68C16B2133008604711F475623F1174D4DB9FF6E29365BC8FB7E89DDCC0FCED679646753BD97F760A7A1DE31B1B7FF420940F11FE333E6"),
		mustSigFromString("9250784E622685E76F438F82A151C94B35ADA64EF964D21C695714EA3C350EC33F8A5B82542181AB4D2EDFE16D17EEE50D8D265E3D54D3EFFCA5B5FF784B620DE84E36386BD52D1458FBB42190A590059C32492529ABDF4057B20AC2934CD4DD"),
		mustSigFromString("A7B77834106D4B580C44F26E4FB9112D70C127A8A907D5BEBC474BC7866BFE56C2C20EE47196D25561785C2E63620B721778F18503C1DA4EFE1D371C191877B738BAECAD944AEF4F2DB57BDB75EF3FFCD7C1877EDBF6409A357ACCE7B4BC06DF"),
		mustSigFromString("B4DF74E20FDF893E8677C0CBB6D8E4D7C8592700ED1BFDB7B9CAB43ECC8C388C506E0326555865F005555E3A82BECD621183DE2DC068E981A8F00282A7B80F8D757C90CC172DFAB43C1CE9AB77C00BA6116C7C9C232C8C62B9B53A355113DE43"),
		mustSigFromString("B3EA5DC7C2D88E3BEF75E8DCE246FC8C134D8BBE496F1D9202D55E090D5788710DD00071579BD2D063F79186D64671D60C3E3489CF25E52FE81A3913DE35801EED661806A5793C63A0EEFF9A33F01FACECC284FD3D98654156FB626099F65C40"),
		mustSigFromString("834D4B8307EDB814B23DD67513E86A0722DB2F11275840BB215D9DF45A6CE0842CEF002D9BBC201F034819C1737B269A12D95D6B6655AD651E53E6A5031BB5B953A01A02DDC5B1A1CC01263F089B10F0F091D9BA001600EC4124FC45B6833E08"),
		mustSigFromString("893C6A6006E10BF3D645727E9076C893ED876DA0586001EF1DCB1DD321816BCC1D1FB1AF2588B28DE5CA9A4FF9D09A3413A4D13A6048EC36073A6794FB6CB12F4797A19776E0420704A8BD43225CB29A53A6899B16D51F10B5AC70C3DACEE2FD"),
		mustSigFromString("AD4481E5048203610CAB7B133FBE4BBD1EC7A5A25709D062FFCCFBE00C18C9AE1D180E07E90247EDF185207BBFC70947192F3115E7095196C04C3FFF59EC6CF6361DC87FB9F1C37D4C280EACAF7A62AE171BC2FEBF85C6A2FA2366A70EE94795"),
		mustSigFromString("B0E5D58642BD07FC32A9F04EFDD501C4BE9B91CEB40EAB4F420F4A1283B20FC57811A61B12B5BB01AC05760FBA7BD7401720899B9356B8F36AC814748296C95120EBDAFD7C2AB3FC566818C3E67F342C87ADF738B7A255A6A7F09D91516533F9"),
		mustSigFromString("99B624FB2D9DD1FAE6DF94CB16A9D58303462D3A872C4E616C6DD257DD602BE511BAF994A0F3B198979C03DE01137DD00A48B3EDCBB381128862400B7DCBE91C6F623E3F51E6009AB59FC57AB0F4BD45A614F8FFD9A38BF3F200BB97B54C0C5B"),
	}
	return llmqTestCaseData{
		n:                10,
		m:                6,
		proTxHashes:      proTxHashes,
		sigShares:        sigShares,
		ids:              proTxHashes2BLSHashes(proTxHashes),
		signHash:         mustHashFromString("b6d8ee31bbd375dfd55d5fb4b02cfccc68709e64f4c5ffcd3895ceb46540311d"),
		wantThresholdSig: mustSigFromString("881a6b10455975518ca71d2e1cdf2a8425bb5cf03a142b157cd0374f2e84e30e435742968ee66be765fb9bd09497210b19bd454694925af35a0dad0ae6a90974d7c76292b2c6f2e3e69458f4c9be27856b43967340f3d003ca0c99105380a366"),
		pkThreshold:      mustPKFromString("b937d87d0bccb85cda4b400e6addd84f3d49dc382d65b7d0ebbf5eed704a681dde992ec8e3fcd144a53914ec0e376f26"),
	}
}

func shuffleSigsAndIDs(sigShares []*bls.G2Element, ids []bls.Hash) {
	for i := len(sigShares) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		sigShares[i], sigShares[j] = sigShares[j], sigShares[i]
		ids[i], ids[j] = ids[j], ids[i]
	}
}

func mustDecodeHex2String(s string) []byte {
	data, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return data
}

func mustPKFromString(hexString string) *bls.G1Element {
	pk, err := bls.G1ElementFromBytes(
		mustDecodeHex2String(hexString),
	)
	if err != nil {
		panic(err)
	}
	return pk
}

func mustSigFromString(hexString string) *bls.G2Element {
	sig, err := bls.G2ElementFromBytes(
		mustDecodeHex2String(hexString),
	)
	if err != nil {
		panic(err)
	}
	return sig
}

func mustHashFromString(hexString string) bls.Hash {
	hash, err := bls.HashFromString(hexString)
	if err != nil {
		panic(err)
	}
	return hash
}

func mustG1ElementFromBytes(data []byte) *bls.G1Element {
	pk, err := bls.G1ElementFromBytes(data)
	if err != nil {
		panic(err)
	}
	return pk
}

func mustG2ElementFromBytes(data []byte) *bls.G2Element {
	sig, err := bls.G2ElementFromBytes(data)
	if err != nil {
		panic(err)
	}
	return sig
}

func proTxHashes2BLSHashes(proTxHashes []crypto.ProTxHash) []bls.Hash {
	ids := make([]bls.Hash, len(proTxHashes))
	for i, proTxHash := range bls12381.ReverseProTxHashes(proTxHashes) {
		copy(ids[i][:], proTxHash.Bytes())
	}
	return ids
}

func proTxHashFromHexString(s string) crypto.ProTxHash {
	data, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return data[0:crypto.ProTxHashSize]
}
