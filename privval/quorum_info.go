package privval

import (
	"encoding/json"
	"strings"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
)

func hardcodedQuorumInfo(quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash) (*btcjson.QuorumInfoResult, error) {
	if quorumType.Name() == "llmq_100_67" &&
		strings.ToLower(quorumHash.String()) == "00000000000000105f2d1ceda3c63d2b677a227d7ed77c5bad3776725cad0002" {
		quorumInfo := btcjson.QuorumInfoResult{}
		err := json.Unmarshal([]byte(quorumInfoJSON), &quorumInfo)
		return &quorumInfo, err
	}

	return nil, nil
}

const quorumInfoJSON = `
{
  "height": 2128872,
  "type": "llmq_100_67",
  "quorumHash": "00000000000000105f2d1ceda3c63d2b677a227d7ed77c5bad3776725cad0002",
  "quorumIndex": 0,
  "minedBlock": "000000000000001266675017f830ffc7f23eec9b083edac3ae652c36a87c6d43",
  "members": [
    {
      "proTxHash": "a4313722c75f52e1b2666c099060be756c024c0b237f22b2153f46703515ec5f",
      "service": "185.215.166.126:9999",
      "pubKeyOperator": "a389c9c2e27ebc42869e985c8399359804e8fa29705ba5003067d61bfa2389829873330ace603f8fbe50cf46b901e1b2",
      "valid": true,
      "pubKeyShare": "a9620fb885966a60a1b6e4f3521c5f6eb60529099b0a1f2f1db0d3dba3b7fa767dd9681695b0bffaa40516230e970d4e"
    },
    {
      "proTxHash": "13498142da3ff383acabe5f3dbf161b51fe65913ee73d2c38e98e6dd73b54b38",
      "service": "65.109.65.126:9999",
      "pubKeyOperator": "b854f486612ad943a6524ad36250bb43ec2472ace84d270d544a5533f9c617998bc1ce0b35afe4056937f7ad2b89d3f5",
      "valid": true,
      "pubKeyShare": "ae2b90dd30eeb5e37a11e4aa55604c2344b4d337408d0619cd65bcebd6b49b01e5a6914965b10eb90250131d015270e3"
    },
    {
      "proTxHash": "e3b68ab54c808c5386e34babdc29dcf7a219a426c307eba15196228016e44862",
      "service": "37.60.236.212:9999",
      "pubKeyOperator": "b0fa253349afdcf35890c8b60844e54619403399704e752dbd202d493fff69a5eb3c24f0ba55d3246d631517d8e4f6fd",
      "valid": true,
      "pubKeyShare": "a32eec3c3664db5ee67d5a65996b5b15542560c8d37667a0dabf980ea4d297a7f67f1e757248c0982e3f3ca28ebd5a0c"
    },
    {
      "proTxHash": "4c2e231fe2732724651d0fd8c5db6a37e2f689c47170cffc52eabb8318d227ef",
      "service": "37.27.67.163:9999",
      "pubKeyOperator": "92e5f1ee8bc8051b9cf44fb4716c54d6b3eafcdf3bc4e11ca28a76f7e4a1d789eace8dd9b1d7b03007dc74464780c77f",
      "valid": true,
      "pubKeyShare": "842ec340a88c5fea22cb544facbdb100020c6b4e98ef03b1be4a6fea3cd03f62e35bef0f0f1cf531e32d59b5a1616505"
    },
    {
      "proTxHash": "feeeba72d96bba076b65f350c48fa03484676e5e1ae3c5b0f2bfd869a690aa87",
      "service": "213.199.44.112:9999",
      "pubKeyOperator": "8283e55bfda843a22bf5622febdd284209812e014666a5de4d8f7da7526ee84dd5e4eec0c85a73a5d73888bc8ba27c6c",
      "valid": true,
      "pubKeyShare": "87f818d6406a356b4071ca3f09517d9696f97b82b3850aad059dbbc187fa1a6ccd01547a05c7240651d39cc83e70ec2e"
    },
    {
      "proTxHash": "2f91eb46842dd12afb2f84418000ccfc583942f52a3334a825519c59cbd3419a",
      "service": "65.21.145.147:9999",
      "pubKeyOperator": "b8296c36e58a4227475e1022961008581c15469b86d00396a882c3a9d0918352e845622cb05c91c00346aa54711fefc8",
      "valid": true,
      "pubKeyShare": "ae0e4eba9a9df2b8613b326f1e65d1519c948f2dc5ecaf76a2dc91e758c3599e724b89e6451205ecb27001aa3b7cb2c7"
    },
    {
      "proTxHash": "5422f2847c2884ca2594f29c54f8fbef2c8f542e128f726f6ec8efa18fdc512b",
      "service": "104.200.24.196:9999",
      "pubKeyOperator": "a0f97343bab7cc1e389844b2c53375d57a85157bf6cf7eb0fb9a31c5cfc44497c127e440d393bebc87950169d7e86320",
      "valid": true,
      "pubKeyShare": "b279fee05e25297792c6172c720e9c52c8a87d221eb7d75ea4c7ad043ec87520aa0210e9c074a38a0c10fc5a90c80821"
    },
    {
      "proTxHash": "706b871255d45be704983d89207ff0844c281e34f8e87e45f8ae173270deb354",
      "service": "95.216.146.18:9999",
      "pubKeyOperator": "a6325ee90ca89f7ac54204c4eb94af561004983a2bccf178573b2d3d7e0db81ccf95411467657a850fc214d4a235084d",
      "valid": true,
      "pubKeyShare": "967fe4873cdf40ed839ecdba65312c349e367ee51d71e957955a0f580430122df62006c102b592bca138f59e7fb51af5"
    },
    {
      "proTxHash": "620ea4839737e145529f392cf1844398188c1e2ce02875038c22d2253415906d",
      "service": "158.220.87.55:9999",
      "pubKeyOperator": "a8d0f0b21fe8410ccf897dffde8b82f71a43bd04e5a25cf625ea8e1197fcc0853bcce33c2a9ed3724dab993238662eb8",
      "valid": true,
      "pubKeyShare": "abc9d04632f635f425aad64f740ee9a4cfb77c776ae8e54c8b3d74ac8133f4f5dd66350d28218e5e62338f03524752a1"
    },
    {
      "proTxHash": "9c55a18619ad89c1498139bb370ae68665ee68d95a113326687375dbeed80b20",
      "service": "109.199.122.22:9999",
      "pubKeyOperator": "b8cd957a00042c90866fb561965991942571acfece531851c05d8b6ef1af2150c1529ed9e37da03a413f4cd1f148af8d",
      "valid": true,
      "pubKeyShare": "93ba90da894bc2a44c3365063e94e79ea26c1547c4ad1621f016015b4ce6be28234303b9d22e94b3af320905bb7013cf"
    },
    {
      "proTxHash": "fc08e30e64483af9fb1190ad17386dbccaf378daf9f4accbf26bef12d0d55508",
      "service": "134.255.182.185:9999",
      "pubKeyOperator": "aea508184a7140d9fac8dafb92e14b5a207cb28530a78aa7cc9088a4c0bfbc167679274222fcf759e63d202ba45682dd",
      "valid": true,
      "pubKeyShare": "b2ff88df8963853eab16429d0a4c8058886701b8d51115f49efec98a3bcc800cc5123386a86317107b4b9a9123d1fd97"
    },
    {
      "proTxHash": "542ea6807c3391fc3893bdaefacc14341647af15da0f856bfc323ea2342810fc",
      "service": "37.60.236.201:9999",
      "pubKeyOperator": "95ea7d481dc92273d0208589407fcb9cc5f09dafe263b8dfde32eec6fd7bf4983d2fd31aa93b67c618c2f2ae8be00d4c",
      "valid": true,
      "pubKeyShare": "b4a4766060c8241cd222af9747d87ebf192b5dcf646a161b565f2bdb82bfe56d9cf7e1faf9565527169987c9ff39d30d"
    },
    {
      "proTxHash": "c3a213b255a6321c441fa6edd0c6c47c774733c946a6fa619910b825226ecbcc",
      "service": "134.255.183.248:9999",
      "pubKeyOperator": "8fb814cbd8ec2c48200232cc5b2eda9662a07cae533d96f6fd3fdf414ba298a4cfdc9e65766e9cc8ac63b19a5845ac40",
      "valid": true,
      "pubKeyShare": "b113754887669a3afbf8f33c4c268f49d917cef482c3bc41303ccdea68abca3b32ea1ab55b6c84a0d1e045fde82e04dc"
    },
    {
      "proTxHash": "c646cdbf76e5d5b77ef99f3806b23bbffe885985d0bce82b3259742048bd55e1",
      "service": "146.59.6.50:9999",
      "pubKeyOperator": "b3dc5af8e7d0ffc416b5879da121f6df6a382c06b03143e6724e619fd599c8e4009f6d5592c7a13633063127578713c2",
      "valid": true,
      "pubKeyShare": "a6d327338f55008af76050025dc9588802f7b6646c945f67ea64b717561099ba98068f99b7ef335c4df8521477f499c5"
    },
    {
      "proTxHash": "d23104cb8d1019500523e8dab780b9635a7a2e9e48779d62063cd1053db2f4c6",
      "service": "46.254.241.7:9999",
      "pubKeyOperator": "8ba29c3ddfa4a7e22f779810aca6ae991ddd06f7a53d57ba1478607132caecb4841b27e7b4c8cb5baf262d136b506acf",
      "valid": true,
      "pubKeyShare": "a512f6022532813ed0858b1eac839b6983f00300b774f5446a6276ac8e4fb0054193dd5fd97316f0bda9f9ea6830e93d"
    },
    {
      "proTxHash": "d353cdcae53071401345c38ab482935cb62442be2174a1d3210ca356c9017eff",
      "service": "114.132.172.215:9999",
      "pubKeyOperator": "87ea31e0e46c5c74d3978bd4243229b9d003f56294459115e2abc01da6da1072459ce6ffba23d5d79f1852472dcb505b",
      "valid": true,
      "pubKeyShare": "9861f8db6909635f562225f1923d4b596259290a6e031d249c15095f1574b2978138bd9d33a87c650350ce9497f404cb"
    },
    {
      "proTxHash": "61569ed796c4e1e3e1f1cf34306526ab967feb916242cf9e9b251206fed9f656",
      "service": "108.160.135.149:9999",
      "pubKeyOperator": "a3b5d3691a867c6a68ec67e3095278611858c90a74d504c2840111f03bea1c2acaf70e2edf37bca6f28cc907609d22d9",
      "valid": true,
      "pubKeyShare": "aeed64f45c73db4b702b86ca66b828b291ea72822341368cae1c18f84337b492c0871e0bdd4522ecaa55917b42b34ff6"
    },
    {
      "proTxHash": "e08e168225f8c14b149b781ecafb320618a5f22c759ba8f8054caba82c78df3f",
      "service": "93.190.140.162:9999",
      "pubKeyOperator": "96ba1fdab8f697eb1fa7acdbe2621b389b65389ef9408a721d7217a50531f1bb049a8c6ffea0be3e40e38fd6592b1ccf",
      "valid": true,
      "pubKeyShare": "81a2269a23d9cb16cb4f7d62b05050e55e8d81033bd20b627f59241c2dc557b9210d1bfeef0e182849f18feb7000983d"
    },
    {
      "proTxHash": "be1cfde128dcfb63035219b03cee7ef3c7cd9c483224dc811e8db64a907a7c57",
      "service": "194.135.93.236:9999",
      "pubKeyOperator": "b85108eb4b62003fbcc3c42737b721a496a5f18cad231bf821628c5fdd779e2a0243c332973afd4c03f6ca1a092473d2",
      "valid": true,
      "pubKeyShare": "8e1552a5ef99fbf606c0714211c2eb76ad54fa659966b420822fa5c7495b12c89a2ad746f74d4bda413870fdcb5d784a"
    },
    {
      "proTxHash": "db5d1d4ad81f1f735bb6cc2561a9c1eabb1a3cd20395508f4fdb36f70ebd4f53",
      "service": "45.77.99.172:9999",
      "pubKeyOperator": "b911d701a1d6c11c32414c577e244ba209b1ccab558f188919d82faba5416a3770f6351eea62c78502259c599c2a224b",
      "valid": true,
      "pubKeyShare": "a9dd639ec0114ea3949ad1d9164284c85dd180f131633ce7a1d7fd1ee91904a68ab5a5bc4002da302cebb20bf49a3a42"
    },
    {
      "proTxHash": "57eb2627121afff87b1378dedef3b4b36984821316ed720ebc9a7beb557ee722",
      "service": "93.190.140.101:9999",
      "pubKeyOperator": "b44cd1ce013cc74395dbd8ae3bf131951bddac4e08a7b83cde543835fe1a623d4eca8f01d9677fcda680a6d8a373c75d",
      "valid": true,
      "pubKeyShare": "85c126f427250065db6a944df3ab8ea1c6fbd938a5064112b36108e37685d5444a6350fb924aadb09893f1b965f2ec4c"
    },
    {
      "proTxHash": "9669de13a19f9b17e505c7220ea91bba016a39f836d0b74dae97be2f1a52a1ec",
      "service": "185.198.234.25:9999",
      "pubKeyOperator": "abc9ec46770277b0b9c695b134ea634c499b8f763b9dfc103bb2bbaa2a2f028e40d5ba73f68c442588ea942614af2586",
      "valid": true,
      "pubKeyShare": "98858f5ccb47e480efc3216d5b6fc839aa589ebab0df4dd739526fd011724c20441eac858b48600ad3e1648abe4d8da3"
    },
    {
      "proTxHash": "47aab8e3800b1ce84577bea9e92c602794de4810d75caedaef264e03804ad41a",
      "service": "93.190.140.114:9999",
      "pubKeyOperator": "9814757029e2483b972a6c19249fa6c5708ddfa51a7a1f82c98bba3ebb85f1657bad1b11468d99dfda8bf38976abc044",
      "valid": true,
      "pubKeyShare": "8a00444b35a4bf4cf06460b3f4c26dbbb60df7ba76811d1ec4f5c2c8199e46d68f03943884fa47738f358e59001be7e3"
    },
    {
      "proTxHash": "0d04d0b12b9c2d3cdffc3e4a59b8ef0e1cc5b165e51c1ad44ee8afed418573e6",
      "service": "37.60.244.220:9999",
      "pubKeyOperator": "91dab350a9f7195f274564b94448f9aec0677bf66d5a0af1e3ceed42deb2081861097468c51b611f868fe510dcb7f55e",
      "valid": true,
      "pubKeyShare": "b36f570a0a19a476d7e08ae1909cc1692b545c10a69459b578a0f7ac330a17b74a3239bef5ad0f1adb9a0bb2c47b8980"
    },
    {
      "proTxHash": "272c13b64739b9853979edeece1e3271bf94787122a8a201d253dafa9d4a7b7a",
      "service": "130.162.233.186:9999",
      "pubKeyOperator": "96b1223d109c50e25ecb947c62398d0eaa6db88f8ac9c93f4d44e77e7303fbc3ae8e8734ec11874323e3ec912ad78781",
      "valid": true,
      "pubKeyShare": "a203868cb365cb0e4d34c5d8a4703f335e3286542bfdfd94d8755d5c03ae9efd4b0776c2f2168f2c1ccb4cb62e1639d6"
    },
    {
      "proTxHash": "87f398c274ae7f07e7cc86a4a5f5ab6e681be04143556f8df875c1db4473edff",
      "service": "157.66.81.218:9999",
      "pubKeyOperator": "b26255fdb33cd5741640458c65bdf051e3bf927b871440b088d4e06a1eb7b49b560834fbaa6f0296a1f9e57e21b64ccb",
      "valid": true,
      "pubKeyShare": "a8262191aa70b3c97901978475b1044629e96d597cf09a2c2d3843306ebc7146352505862bd20d37009220a0a7ba517f"
    },
    {
      "proTxHash": "a3a4a419c66b1ee40ee3409ad8b9296b038755ebcc0781f3b8ed63de2c25745d",
      "service": "213.199.35.6:9999",
      "pubKeyOperator": "af5199e7a2627ae33ae62a353b99f5fa6d12073b640efd4213298d2726597f6582d06eba1fa435ce265598f0d8404107",
      "valid": true,
      "pubKeyShare": "81af7a7f133ebb8f941e7d2c3e791d4a066acb2f9b47cda01ff56846a1bb342438505ac9268e2ac10e685395c37e424a"
    },
    {
      "proTxHash": "0e9a4f75c4bb0ceea1eec28e2b730d81c192a5c386c46886ec48bdceaa8d3ff2",
      "service": "49.13.193.251:9999",
      "pubKeyOperator": "a3b27b11ad87a91698358165599d7a1110e2e1b711476570f04b73c18479ebbbe512db2a2272b2d2516d32af7eecc583",
      "valid": true,
      "pubKeyShare": "b49f253b0d1d2c778f53b53088c234cf4a9b99400d690bd6b606a0183ebf7f98da9500a5128c1a8cebab85e69a2b02c5"
    },
    {
      "proTxHash": "b2deadd3bda2551a39b99a868e8f4558c9a9ece2ff80b4361816f1d1a46cd1f5",
      "service": "31.220.84.93:9999",
      "pubKeyOperator": "90ed3b279798232b1b99051716f29e69ad2a6f5901235cd4b5b8b2df287a7b44ea119a48a46cb2d5617852e25a642ed1",
      "valid": true,
      "pubKeyShare": "a161342d066237d0f9b25e0547bd02e608e813bc8cfdcdae3b6037726f81a3331224a6098bf4cc951a796100e3955324"
    },
    {
      "proTxHash": "79f6b7508b8982b883ea1a6e6657e005fc751729bd6ebefae6d5b012c3661323",
      "service": "65.108.74.95:9999",
      "pubKeyOperator": "b9f6ee4e924c3647dc43ca8bfc6821e896fbacc5157130dd89c3d2c3df4a1a21a3974aded03cf540d1ec9f84528c9ece",
      "valid": true,
      "pubKeyShare": "b7b0050ba2a123e67054fc160b0067a8c6009197e987ad5735857a2c226fb0c1aa4eeba1c199bb7e886877529edf8bde"
    },
    {
      "proTxHash": "71f33163403d54af9402b5e9e46384b2f828120f0ec9d370cc08b3aec4e92fd9",
      "service": "93.190.140.111:9999",
      "pubKeyOperator": "b56a94a344a376bc2c8a81b975c6b4567ec2dc428ffb3cde63655681f0cc127ecd27d0097629cf87ca846fde3352cb33",
      "valid": true,
      "pubKeyShare": "b17a99e3fbbca7dec4618864b46ccad1518d983a549ac4fcf59a093c1a6280fc44603eca8b74719f1c6388b5dbdc2f32"
    },
    {
      "proTxHash": "e78966d144d820782ab9f8175f4e4cbb4ad600f45c679d8710070ca8ed7cdc1a",
      "service": "195.201.238.55:9999",
      "pubKeyOperator": "afbab491d67dc742440f92861cc6470c172bfea5006ee2bcc0b2ae8ca1ab549cd5cf5810bfdda23a1c9a8849c7c50b02",
      "valid": true,
      "pubKeyShare": "ad9e1010460a846cb5390d09083cb68f7289f5184db5273d350d4aed3b833b18f9458f47107fa62f04c2c02d7e606a30"
    },
    {
      "proTxHash": "dab0d9af2d70e59ffdf3bfe375c6e9f3d185d2527c0842c7bf2815798a166526",
      "service": "161.97.160.92:9999",
      "pubKeyOperator": "8455cd00d19792377ac915614b06cc46f161662aaab1d5f1e73f3c3cac48a1f2991d75ba14decb308294ceaf7185ef21",
      "valid": true,
      "pubKeyShare": "a5757891ae397526af6e85ad24c240e2ad50e4b0129f2890cb6583d3033527c9df3b12393cbffa04ccf14a5a2bec88aa"
    },
    {
      "proTxHash": "41c2796506348522794b000a5a7d24e2b2395c6fa856cfafedad3dbe564f108a",
      "service": "128.140.107.66:9999",
      "pubKeyOperator": "87667e9c5e91ef8d5e1bfe6a93c445206cef0012eb939d5ef365d94dbe72b1b91c462ea2a80d0edb6d3df88a2f3e1f34",
      "valid": true,
      "pubKeyShare": "9502d8f6ae161c4a153ef228882442e2fe748e3267342e4212852f7dcdc4d958e2453799652e2290afb89cb7eea31636"
    },
    {
      "proTxHash": "a116c0ec761f0542919fbf226b4c8d77a57ef064e09df201dfe9aacb0d9018b6",
      "service": "149.28.247.165:9999",
      "pubKeyOperator": "a1fd2633c2504c990c6715b22f0ccd2863d7827592de6e69ad5842135bcdd782383dc0163f21c5bbe76711b608412ee1",
      "valid": true,
      "pubKeyShare": "a8040ac0387b4468623cbcfdd5f4e01925d541ad2d412d311a76fd27299ef7a5f0a846bf7508b8a63845e0135986e1c4"
    },
    {
      "proTxHash": "16cd333a82c35b71829ad096866f7a0925f9b5350624c1649389ed5f73de25ad",
      "service": "134.255.183.247:9999",
      "pubKeyOperator": "abd4b0b28c5c43a7cc79b10e16e6898a3cc1e08b9b7641973a01cbbed00a0a86d2067dd37ce21c636e3739d248c0b259",
      "valid": true,
      "pubKeyShare": "a084fbf2283ec6c52756b61a4251e5b7ec59c1baacc7bb7172b490785fa9260ea763942cbbfe2c546e8dfa37a59e86cf"
    },
    {
      "proTxHash": "0c0702ee0b2c1095645fc3acc5de92bcdfdbbf6c9e906a1d0e8985a14ac0438d",
      "service": "139.99.173.66:9999",
      "pubKeyOperator": "85a5848a13b4cf30093432e0b4d07243156a69f8d3bb4bd635be49d36fd98e8de3a65391e8f57bd534be801ce43b7cc4",
      "valid": true,
      "pubKeyShare": "ab633a2a67898c1de39a72604771c3c4ace273c1e9827f9956327feb21488ac6666348ca258521d32370680a0aff02df"
    },
    {
      "proTxHash": "3abc901c870c1b808fe672ff3aaecf282c0f2b2a41ec40fc991cb93bc19ae8a4",
      "service": "51.195.118.43:9999",
      "pubKeyOperator": "a1cacd45c162fd7e67d22b838d24955c56fb9db75c28a1d05ca7789c0798a9e7983460eaf64878fe6f8c302d704a831b",
      "valid": true,
      "pubKeyShare": "a1cb2cfab60f445f5e3433e26225ef10ea318737f98194ab446a506ea56d59c5c4f89cef80bc2d181ebc2a1ac33feb4f"
    },
    {
      "proTxHash": "bbf2d6a06a186cefc4bdedc1116a61b39b15edaf29de3a1310c36557638642df",
      "service": "213.199.35.15:9999",
      "pubKeyOperator": "87dec8d5f7e9972e96d10aaefce04550f28f6a34f5a6b061270916293de69af2b7b1ba929ff5aa1f36083f1e99ebbd9b",
      "valid": true,
      "pubKeyShare": "a84c62b058332b501b3533c5669f518433b5284c6641874c3e8e9108f4fb2cb5eb4f81236cab6f0b8f34def72cddb700"
    },
    {
      "proTxHash": "70e8aeea3bd08d782eeeeb771ec4cabe6cac105d2ff4b78c06b2e07ca698fed0",
      "service": "157.10.199.79:9999",
      "pubKeyOperator": "94c363e4fc195cd59e377820c00ac1d1088de511ec3d502dc8d37b0094fc93ad587d1838e9962d7642506293066086fd",
      "valid": true,
      "pubKeyShare": "ab53e9e40b1a56b6c2e296a1590aae2771fca65f97db0b43ed669b70cfddbcc8d4c4f1c0074a57bdbf1c3ba01951772b"
    },
    {
      "proTxHash": "1ff4e7e6f15acb8caa19642e722d62bcfa616d9d7378d18e18f2f88f42a3205f",
      "service": "173.199.71.83:9999",
      "pubKeyOperator": "93ea3f155bf1e987f4c11a4a1efba35b7174c779b1406fd9a17ca6196071fa00a6b595c2cad24c53d38f39b067784bbd",
      "valid": true,
      "pubKeyShare": "8980b4937484ab6ae9e66c81ea55a98819b1c393e9e705df8a7bf625ec0adbf2415f7b4155d8242cd932104cba5cf8fe"
    },
    {
      "proTxHash": "4bee92fb0fd27a3b23b57f67052d18fd18c6e616935545a6d0e691663c836b9c",
      "service": "213.199.35.18:9999",
      "pubKeyOperator": "8e502e00eecb236b6899dba6f5a868c3fca58c010724c8f51e60ab73fd3463d8b21169a4f0a8833f8c6e8db7ea44c575",
      "valid": true,
      "pubKeyShare": "951c02feba945d97ac82b736c2af5e391b41b8763f7fd8b175cf64d5b5b86d9423e08d10182254630e20f77e046a3631"
    },
    {
      "proTxHash": "8954a426836f71a0a04e8321cb3b3487ed404790ca19d29419d15cac02de4038",
      "service": "157.10.199.125:9999",
      "pubKeyOperator": "82af0e5c19bd0f8ebd6d92c648ac4bdf8877ebcb70858decbbb9f9ea38bc623b2029616a54bb5478f2136952ad7ad3ec",
      "valid": true,
      "pubKeyShare": "a6cd29bdd55f3fac994f9afc68dd22147560706cb9f7bd80cc74d914b829f270b56d741d0cfc5686481370288bebdf86"
    },
    {
      "proTxHash": "db8688d31781cf8cbe39dbe2dd5fcb7b3cc172adefc8782317352a4018be75d1",
      "service": "91.107.226.241:9999",
      "pubKeyOperator": "ad28d6797504003f715ef13fc4a89a436155237a604c59f3854f2c0b46d02c180e236a62114119ee39f1c84388e72a1b",
      "valid": true,
      "pubKeyShare": "a2fa8a1aa16b6c9921d789dc748c204d069602c74b39827a2294ca4271f42fe732a26aa663ae12f69eeba90e78ea511b"
    },
    {
      "proTxHash": "aea18dc0d503713e87eb2e6a24636b66dde356dc597210bed08975be14e37db3",
      "service": "185.215.167.70:9999",
      "pubKeyOperator": "a9f90e18d674fdd89a6a423bdc83d7a7f52b3adb8fa9c41ec4f9cf9349d42a7778f07a036dad56ecb9d5a7c8a145491b",
      "valid": true,
      "pubKeyShare": "a95f664741806e28123d111401cecbc70d45fbf60669d6493bd26eb6a4bf944f680404974cc4fbecb062b6f1cb3ad096"
    },
    {
      "proTxHash": "4eba7299acd923212f9ff9faefda1c8268ff2366faf4099cc36fd0db403703b3",
      "service": "95.179.241.182:9999",
      "pubKeyOperator": "826326a01ce533f34e78cae278549f791cbf39cac606c49a790e278a667263ae689b433a9ad817c441462bdf0be1d841",
      "valid": true,
      "pubKeyShare": "975a190edc4e7045409865163348b96bb5984b4d3deda52b977305bb6104994028ad500b266bddb3ba032cda40b90660"
    },
    {
      "proTxHash": "36d014b83c54f35061dcc08178315fca46c951ffa6c606e2c0ac3222bb0d01eb",
      "service": "172.104.90.249:9999",
      "pubKeyOperator": "898b7babc611a4f6ef51cab87722f08d823df46a1c73d1a9a0c17e5b1c1a6d206f2db4975371b60aa9ca1f91a0e6dbf4",
      "valid": true,
      "pubKeyShare": "860563941ad5a7fe8b35cde5424285a3291bf4193a08b4e94d65ac48dff3ceda496b1e7b0a44f620896b72dfbdee280f"
    },
    {
      "proTxHash": "6534b58e8a1c39b3d56a6af2fea0781ab48b2076febc6ccba6efaad6ee46b268",
      "service": "51.77.194.69:9999",
      "pubKeyOperator": "b499a1e66a27405b5bb7ea019e01826474ac0c71fcc0d55625e7fc2c4526cdb2b361aad6a1644522700d66c70b3e037d",
      "valid": true,
      "pubKeyShare": "89d8737a5b151ca2a7dd661e396e34a63e66445b7378ef6fa3d2d4dc9b56b39adb974f5f8a5cb37881d64d27aaf3ef84"
    },
    {
      "proTxHash": "101301a6b8fe9474c7647f51509f0bd90b3ba3d6022cf7c09cb9bccc3ecdae02",
      "service": "51.195.91.9:9999",
      "pubKeyOperator": "aecec472675f3dde140d1b4dca73cd3c9bc509001f922b7abbc63fd5d4e5d26500018bd26f76135c5db024241de7d6c8",
      "valid": true,
      "pubKeyShare": "b30f9b7c27f9fc0767bf32f533c7b0697ddac797ca7d1285a4eb2b2126812aff7777276fb7e1f8bcd9b93eaa01dd118a"
    },
    {
      "proTxHash": "110c7c7bc6d52cdded570a6e3bded9b957fcb2bc68f1f6045ebd61ebe51b0bf4",
      "service": "109.199.123.10:9999",
      "pubKeyOperator": "985c4c8db02ca463dcb38905104afc7e729b29176d3b07b47f187a817edaebf17251ebf15d505b8c93d8c0ac223693bb",
      "valid": true,
      "pubKeyShare": "8d27f67102d05e8a86911a43954f1d528f2356fed6ccec98e0b1c6faf4dabeb2c1ce56e8af913105485654d94161b5d1"
    },
    {
      "proTxHash": "8bd1644adacb12968a913781ec7c5d6ae7ba1d9b6a65217cf45c900ef8053181",
      "service": "213.199.34.250:9999",
      "pubKeyOperator": "b62e8f7b6d221570111aa6d01ca01185666a00e71782fc2923eac8d98e459615fa9118df255225809344f58424f6a730",
      "valid": true,
      "pubKeyShare": "98a3837e9fdf67d35208027b87b1a2c56735e3447cac5ed8f7f0c65d81e19d8748e43c6d144f27782a83a3dd3a6c8618"
    },
    {
      "proTxHash": "f1cc3940e4ed45d1ca63491008127a66749efe1be782586a039f36c6e2949c24",
      "service": "5.75.133.148:9999",
      "pubKeyOperator": "94d803e303f3f5afbe7bd7d4f76b9b3a400118d53e5dc2a782d80caa91b798cceafad647b3e29e0a56a93369e93ee1b1",
      "valid": true,
      "pubKeyShare": "806aa04461d7f53e11aab654a30aaac6bcc0864b472776bf1c16eeaeb29d697a9e8db19217be34eed9d4babd6115b6de"
    },
    {
      "proTxHash": "ea4a6971496f94644600fff68fab26e76c01496415fee28eab224846c558e09c",
      "service": "178.157.91.184:9999",
      "pubKeyOperator": "b37119caea474ef82dd3673284b70c294e89a66c575406d6d927cfc3173331388d4d4ba250755d9da45993f2be00eb79",
      "valid": true,
      "pubKeyShare": "9335e1d0d968ff3651962e696f7d74bbfc9c741dd7f35c7315b34b255b48fd6689e27c8b3f54ec19ce9868e60c611854"
    },
    {
      "proTxHash": "8dc85b5b4d6f424eab1f23fd8a7d9340ecea1a74f3e6d98debaa98251774f3a9",
      "service": "109.199.123.12:9999",
      "pubKeyOperator": "b61237822795c194f70d5324df58241081f981878b633ae90e01a3f21c0a7f113ff146e71482046bdf2f64e798d3056e",
      "valid": true,
      "pubKeyShare": "907c6e3586bb77643c4d01b57eee5b31ae9f54372699776deae1dc443ee6b7caf462c53cff80597777dbb8e0a2bede79"
    },
    {
      "proTxHash": "64c9c11cfe31be3c1fec56ffb2a756cb352c164f3fce1c0a9f991ab797b20d90",
      "service": "198.7.115.43:9999",
      "pubKeyOperator": "abf8d7c9e5faa19a6c82f274aecb942fc2f5d34216f074c8ee0a79fade914d1300b94f6dad8dc92fa1a65e088db7a7e3",
      "valid": true,
      "pubKeyShare": "b23c61edca776769ca755b3b3c4ec4390cae20300b52a9a6a2e9cce0d6daf277808520d099ebde9ee5c7104b5899b90c"
    },
    {
      "proTxHash": "0a1dc4c81b3ba19229f698e454dc41ee9832343fbe9e9a4fbf8a91b4dc39c291",
      "service": "216.238.99.9:9999",
      "pubKeyOperator": "b529abf7ed329e3f7a75a032764176ccf867a9fceb03fc15cbd144e896974acd4e1af756bc8ec4efb667d35577ccbe8c",
      "valid": true,
      "pubKeyShare": "b4c7d312f85e0dc4757aa737cf86fe74a0daf40c0f6fdf92a440140569db697cdf075913f5c45c70992b98bc304bb135"
    },
    {
      "proTxHash": "9e3ff349fbe7944a99b77764f7d03a3d765ce669bc3c07637014c683ce324cc0",
      "service": "134.255.182.186:9999",
      "pubKeyOperator": "b3291ae3c6fd9be650a427c32f5c46396f2bf1bfd65834ff4cdcd79cafb6d560c8a2e3a365892dd5d6b24fe7cf92d234",
      "valid": true,
      "pubKeyShare": "83e1b86994f15b4da5a1c5bff0a3b899ea84a82508e96d87272ed004f53ce107019405bd36128b70bb5badfcefb85094"
    },
    {
      "proTxHash": "ab65e314b9d04d6bb066fac4d35e62f74ac8f52a4c1134301354428dffb949d8",
      "service": "81.17.101.141:9999",
      "pubKeyOperator": "85445596c12406d7444effaaa74c8a01ed9583795a042c96184712cb095b9f9a43b6e738e07e99392d8bf10b8d45e540",
      "valid": true,
      "pubKeyShare": "a3767a851e0ec53956dab719205e98cdaaf85bd9d51f3e1942b2fae3f576d213fd926289185485689741e3eba7c6985b"
    },
    {
      "proTxHash": "ce99b1297bf30cd84ce448c08fba3580981febb9929d8baf271d5e53daf2ea70",
      "service": "51.83.191.208:9999",
      "pubKeyOperator": "8ff732e75551d9392d54236c679e71e2fa51d1a40c723964dd8f93f4288d63508c06808d57aacae1ab033c32bf357149",
      "valid": true,
      "pubKeyShare": "960009f69319492471dee66c63fdf85f811d7f7ad2d2c8283521a78eba71682b97562e9d514559d12a6b565ed8455723"
    },
    {
      "proTxHash": "fb039ef63df8d1989aec4f87b728e7179bade6d1b429ab0c952d541c66eef4c1",
      "service": "157.66.81.162:9999",
      "pubKeyOperator": "9837e44db6df192e71011a242f34a1ff969758a428103eb1ed0d356a004aeed32d66fa4a9471668eb51e941720865a91",
      "valid": true,
      "pubKeyShare": "87f58dedca460b5ab56bc319239d4c91cd73a49f7fbe90e8d1681afacfea359b57d19f9b0e39f219162544b44c405953"
    },
    {
      "proTxHash": "74056fe5b57c33612cc688a2d3053273d40baa36cbf0ef2ba29e096e1377302f",
      "service": "134.255.182.187:9999",
      "pubKeyOperator": "b451aa29106bbf5291f36e5f67b7497ce7e4fad8eb0439f616195ee2e9818d5f2e5a851bf12e18abaf6d6ecd7ba06e0b",
      "valid": true,
      "pubKeyShare": "94185861ccaf0774ed8a45eca234d60735b497b828d71d6fe039cef263393e80779bcbf093c4cb4a8e027205858e97d9"
    },
    {
      "proTxHash": "a0c9b5138ac6709cc8220797fe818338788f85378934316d298c03e392d32b1e",
      "service": "146.59.4.9:9999",
      "pubKeyOperator": "b26c789c9fc11b03afc353ef8da03c0ff93ec03ae62ab18fd244610abe736998df539d6f784eefaa77d7355570e537b7",
      "valid": true,
      "pubKeyShare": "b1b81de2f1a2e6c8267abf875c6a9a065daddd1cac61b5670638a96066d09978d6173b6ef5fe02b1471ad6e6c7f82042"
    },
    {
      "proTxHash": "8bc76ca7a979ded6171e6a0bd9bd2a43dfa052e7b54b092d9862db52efe38726",
      "service": "64.176.165.102:9999",
      "pubKeyOperator": "876aa7b7d10602180233c84bef6edd4a8de51ba0366699efae37db82b64e227bc24d8a57b914d306587b7fd65c00e45e",
      "valid": true,
      "pubKeyShare": "adf36594617fd8d3438489df5df634c1f2618c0626ca430be202da400daaa6861880aaf59e6fa1a7314b13b15774f3f7"
    },
    {
      "proTxHash": "07bffaa884f78cfb549d6557065d01a5c537b39434b2b2767a6114e5d9d73ccb",
      "service": "37.27.67.159:9999",
      "pubKeyOperator": "89717a29aa87fbee26a8e1ff647350f3d950129adb49adc386a042984821d823d3d6b39e9b6669c243051a86fc87edaa",
      "valid": true,
      "pubKeyShare": "97423dee977585a1db03ce209c9db2e302e029adcd545e1dbc81d29b0016c498a1c063a9ddce48ac81dedc076141db32"
    },
    {
      "proTxHash": "936876aea72924467daa99a22205e3d94462b9cae824e4127ef1d554f397d6e4",
      "service": "95.179.159.65:9999",
      "pubKeyOperator": "a190c08941ca57708773d090250337d0f9aec515138223001e10385c3a3dac8bf2b2d86b89547c1c253968c3dba37781",
      "valid": true,
      "pubKeyShare": "ad2740cb7b80597182c820f832cc7dd174f35539d9748fb706fbf0565ba92a21c68ea50af8a3c9205b1a6d54f33d8196"
    },
    {
      "proTxHash": "62472f43d140786abf3ea0a02f5d9879604ffe7f9552529015bff335c3382159",
      "service": "49.13.154.121:9999",
      "pubKeyOperator": "95a7104422a6f3fbce5a736c7024c6623de4a48b00f00b23ca75cbfc30525a6712411a5a577920fc571dce25f2019d17",
      "valid": true,
      "pubKeyShare": "a1b4d9ddbb5f0c485ba2dceaa6d7a5d6ffb55700faa8fd6de75eee8ad2b055f5231379ce80038abfbd95088e30944d8b"
    },
    {
      "proTxHash": "edd39830c5f42cae9c259f4bd4f26646c3cca5b08f292e51dcf8bf3c24ea60f7",
      "service": "91.107.204.136:9999",
      "pubKeyOperator": "992cf6d56eaf74e403fc8b7bc50ad93f55b5d8224aa1b28b956fc41d306915f7237936eec7bad450412b20e0912aba9d",
      "valid": true,
      "pubKeyShare": "8ee3a2b62c0aabbb6a15cae96ee7aa3264a5f9e8fa92f13bea538913cfe837774ec92188ca084d381c1158ec502f405f"
    },
    {
      "proTxHash": "24dc24a79e14ecfc945e04d8a69e6d0ef922f43cc166c1dfbc6a22a448bb4397",
      "service": "157.66.81.130:9999",
      "pubKeyOperator": "91168dc7651c7d875514196d073e54aa23879e01a0a81b17fac7b855b6213932ed1839db12c88a8668d6485fcebf0a07",
      "valid": true,
      "pubKeyShare": "a2107b6c38df040a5fb99baf0af5283d339572c96014582253b5f8807267e4ac87806afc5d6c9f6acd588f1ea80a35e8"
    },
    {
      "proTxHash": "8da027b6f709c174da865c86c77877cebf75432b419b99575684cfce0e842220",
      "service": "185.217.127.139:9999",
      "pubKeyOperator": "82288d99909eb37b7c9a566c001d2ff50511a889dd647a1b7ea881f77b8b5a7d99a2fa1718759465b037d2f781a5be3a",
      "valid": true,
      "pubKeyShare": "a5c3edfef6d17c17d9f9f33d4275c17197ae2c9208265806fc659109d531bc19b2be7c6ef4ea565853f9cfe27d8f8da6"
    },
    {
      "proTxHash": "12f35c3050a1e08b9a79002218a0f5f7b2e9709cf82cfdf9ab04c74dbd16cc5a",
      "service": "135.181.110.216:9999",
      "pubKeyOperator": "b5164436429e3439e883d80eb0e19de8e37d264b99067afefe1e61b07d288be9f5d6dbb96766830522c3cb6be741d5b5",
      "valid": true,
      "pubKeyShare": "907d76c9df5b15135a977911dc2af8ee4048d314481e167f8e82f0e61c202ae5a6544b87dfc3a73b5493d8f1001a60ee"
    },
    {
      "proTxHash": "a3212bb52d08b098aa4a65b0cd502fc2c87a8e78f987b3e98c16b769c72c1b08",
      "service": "51.195.91.7:9999",
      "pubKeyOperator": "812d74415ec6f9aa56d3cc66009c7aa7c7057e969e03a537a3c9638d99acc89427523865b0468a12b50a572fd5165409",
      "valid": true,
      "pubKeyShare": "96d3209b0af483e041f9067f05907933a43bbfa2252bec76bdd138b386350e50a428ef000b8454b702b9fe3a6290ac22"
    },
    {
      "proTxHash": "192e7cd62fbfe2bbf7d9a5972c209883e65332b5b860a64236b79268baa9d051",
      "service": "157.10.199.82:9999",
      "pubKeyOperator": "992c84adad7de610a4ecb2d3a370d584cc6c9e8b83607e1dfa889cb7d5aac1bfb62fcad2ca56b724d350af4fd9d543f1",
      "valid": true,
      "pubKeyShare": "93b52d858ed21387db34859c98750dcf8a15b7c92398e73f5144f4b6e57bb10b730a74d362c1904a40ec4ca751384be3"
    },
    {
      "proTxHash": "35978e1a9095afc9dcd72925a001b8b38331154fa2acaa7d2b00d6f81165d2e8",
      "service": "139.84.137.143:9999",
      "pubKeyOperator": "b4f385de097dfda3d5cbbfa870ee6e6e95e35ad26d619ce257fae1f458a21a2d0c2b6e7e0982a2d78fc662a69180dead",
      "valid": true,
      "pubKeyShare": "a510393b6ac26754b1c5ab8587b12997036041cbb7f8e52a88724af43c2b8e4384ca5f10491edc725880f50e1471718f"
    },
    {
      "proTxHash": "093c7924743d735ba2f73d6c0dada30babadd8d083fd35106ab31561f850d31a",
      "service": "188.245.90.255:9999",
      "pubKeyOperator": "9323d954c8ce5744a030356fa68f7c89719fc117ba39859d6ef9335d1f26faf67a1bbbd6718a7004086eb3e3aee4c1e1",
      "valid": true,
      "pubKeyShare": "b2a583130fd684c2acf21b1ff45d821d949135dceb310d33c32d4c8333fdab07aff447f0ddd0aba53a92f10826148ecf"
    },
    {
      "proTxHash": "3e596421618da23ec6700771c2f4cf819fd9ddec753f7889c96f7d1ecb6f9c40",
      "service": "149.28.241.190:9999",
      "pubKeyOperator": "a18ab76ac05b494c300ba486a745cf6a34598d297f24a1db01750241630e9f04423a0b9f28d6557b87ee459e0759c29d",
      "valid": true,
      "pubKeyShare": "8469668d9460e8828e36b453c052ea4de08cd39ee70e467fe75ca2a5c46689b21347ee17027102d16c805e3faa81372b"
    },
    {
      "proTxHash": "74a08086d3bf920c0e56748bf2d0cfed1419f251d598fdcfad1213391d23db09",
      "service": "173.249.53.139:9999",
      "pubKeyOperator": "b6c673df3de1344b10160ce05099b1d477c40bcadb444a10c86c205bf0b4470874e64e8b92591f582577e1f207ee2b1e",
      "valid": true,
      "pubKeyShare": "abf1b3392b1867cbf7095bc85b8204292673b0172389ddd0e078de05757640b3b5e1bd00fe76871dd5befd89357d2d65"
    },
    {
      "proTxHash": "bf86442200d72693d7f1e6bf5fac0325d45fc652bbdbc3be66da142164eecc5f",
      "service": "51.83.70.84:9999",
      "pubKeyOperator": "ab452fcc169f203b1cdc0ada5657ecc9df93e6dcfe91a3a5b7ee2065ff10d1f533f967b9415133bf81dbcd024926c5e6",
      "valid": true,
      "pubKeyShare": "b754978caa1297f3e11951663b1e32c0227245fa2fa5cb8172816134546c5a2e4ab2a5916c1560cd9f7de86c26dc8854"
    },
    {
      "proTxHash": "7494ab33759421b6e04e52a664665c55d279a70a06b07b68a6356dda2ea54b81",
      "service": "157.90.238.161:9999",
      "pubKeyOperator": "9380fef0df25c5b6f1cf2970ce65123e5a60889e4c9779613e3311931458a6834c162e99e8fc49f31dfb93d19a2fc35b",
      "valid": true,
      "pubKeyShare": "a3be8acacef39215cc2fd38de65bc53603f6e445f75c4c655d35faa016ca21ed02afabdef32e5a22331426a31a006327"
    },
    {
      "proTxHash": "698733bb0872c94a08cc02df294e106c542dbd2dc0393c34aa71bbdfa5583329",
      "service": "37.60.236.151:9999",
      "pubKeyOperator": "819a0396e0dbfcd22d5c992179965316bcf13110a1b70a084511dc38870ab73056b50d2a8f1780d61cfe28c13e013f21",
      "valid": true,
      "pubKeyShare": "93df2dbdee4b787414d088b765bd660ed27f63ce9a3d2f73aa1c3b3f1fa34e48130e4fbd5de29777a56d241961395501"
    },
    {
      "proTxHash": "a20f3edb99394ad9d93997567599baac88abe28992276b602bea48d5eb4fc2a9",
      "service": "139.180.155.143:9999",
      "pubKeyOperator": "802afc55f52e920139403433c960ea72d9cf5bb119ab8bcdd715f895fd1a367036cecbf8b85970f317e54895c152218d",
      "valid": true,
      "pubKeyShare": "8a4cdeae47be2f1b506c82cb5106b394767400c1f5491034ec7913d849a98eb1b4313ef4b9c23fa549274e623f506fc3"
    },
    {
      "proTxHash": "0c3774b735180708d90a53d5fc99a2699a9a53cc21cf27505d1a184e1ee836bc",
      "service": "213.199.34.248:9999",
      "pubKeyOperator": "a851bbaa9f4c4606dba0f78424878e6730a452f4ac8254ea50f7eb08f84465fb907f8448090948cdb7bbab16c341872d",
      "valid": true,
      "pubKeyShare": "8792eed40cf50cae982c2412f7a82b1f49bc637bb993b3d0647061c409b9fab2b227b5c2979ff255ce00f5771e5f3afc"
    },
    {
      "proTxHash": "b5c4d7bba1b11f98497c419c4d81b72a525fe30ae788028c570dd4eb77255e77",
      "service": "167.179.90.255:9999",
      "pubKeyOperator": "b922a998c2e1aa20f1e973fd597ac00a22f8c0520d5d940e3a4e3e13b1646206b62ecb224c6b423dde339dd09467123f",
      "valid": true,
      "pubKeyShare": "a041cfae8c4ee786f9ca8491038c3cd11ca8eba566419175cbbf9b8fb52ba4e01b898a876102b144f7bf0d000ed02726"
    },
    {
      "proTxHash": "2d80ca81d3ce72365889060a9c5dcc30b04e7075f589552739188ecac01b01d0",
      "service": "70.34.206.123:9999",
      "pubKeyOperator": "a2157578ab336cb920e1494bfc5b1797c9d76ac376272c6c73965a6f8377bb0ea08186b9f2bb1b742bef95770a67b29b",
      "valid": true,
      "pubKeyShare": "af59fa38f480774c085bf69f5c25447ec9f48f9a67086e0025b54ce4cc2350ba5b41c562adb34dbcfbc47440268d4f1b"
    },
    {
      "proTxHash": "52fc4d740991fa557c439a5d553ba0bd71f84675f41268568c33158be6405f22",
      "service": "147.135.199.138:9999",
      "pubKeyOperator": "b6828be7af0fa0e173d8ec5c8733135e29af030181fdff59b7f5af49c7e8abe95e29cf7850defe8532a65cf02baf7349",
      "valid": true,
      "pubKeyShare": "ad90e9266d3f000be7f87170affaf84c2c94a0723819b91d5bfb758fb4de2ca772509f2366d7fb4a542f7f19175a1a42"
    },
    {
      "proTxHash": "ffa4a74c58aa0002696df1a7d2562b5a36f5116581a0df1a91500a9664985f52",
      "service": "37.60.236.161:9999",
      "pubKeyOperator": "872d9d5f9a7fa1a24501dadd35537c53224582815894947265d0c8e93a02eabd62224c80805f37a80748ee8c6aa662ea",
      "valid": true,
      "pubKeyShare": "aa1d8b2cb5640df07195b8965447519f35583e6a43cf1e3894b9693d9566066b6e7815ef0b832afaeea001dc8e257cf4"
    },
    {
      "proTxHash": "e5d857b3d1cfa3728be68f34f7592cb1f4318367b4549affaf2cd0cb3e47e516",
      "service": "51.89.216.210:9999",
      "pubKeyOperator": "b4f4915d9596f2fb65958f9d5cef6c5df215b7e2ad27ceb352fba38331d8bfa9faee1e9e982404d807a98f19e87bbf65",
      "valid": true,
      "pubKeyShare": "af50265bafb83167f4403b24b63b9eb45fc1ec7367f45e2c8df572d41a633e8ae5d60946b5b226448d1482daf18cb9df"
    },
    {
      "proTxHash": "ce6ca219907bdf643803b614dae6e52ca3a2a43910be8d478a0a1d303b987d9c",
      "service": "95.179.139.125:9999",
      "pubKeyOperator": "910ad7703f1e943963031d2dad7c9430269259a4b525d0f3c7266abbfa9a44ccdcbbaca7aa8e45c658535ecba21cfe65",
      "valid": true,
      "pubKeyShare": "a82259c5810238090bbe41a467be149e5802c6c468e06317716bb8ca0575b46b6f3a1c325e103e350865a0192a9da630"
    },
    {
      "proTxHash": "28cdae2272c4e52901cb76097e4109ff96d442a9d323f37cd4d325a07a0d49dd",
      "service": "37.27.67.156:9999",
      "pubKeyOperator": "9183c2be1a60f242b4a9d9882646e6bb06e0c44e8e8d78b8ca1e763e67ef6d14e57388a461057e210ac15f084c90899e",
      "valid": true,
      "pubKeyShare": "91a97e1ceeacff6fe192171e050fdb1d321017672c17051f30cc991890934bb83a9926cdcb23b47ea0e8a65dc30b0ae5"
    },
    {
      "proTxHash": "f46c5b8627939527541e293b16ad035fdc79d282e328c412fb80c1133ba593af",
      "service": "192.175.127.198:9999",
      "pubKeyOperator": "a61f2c80b0f3d2b85347589ef6b0106910a5812479e8b0096c5e23be38b77160439ad8e9d80f42b286b3639aabb44b0b",
      "valid": true,
      "pubKeyShare": "ae1b29c8a3d3f298d608e3b86d4f169f7f173f989207b164e33b661d8799aeb74e2b1c208d50f077af9e2607d0c5f2ff"
    },
    {
      "proTxHash": "e7ea6db743a83a34825d0958c7366ddd80053d7e97232597f81f8e0d85570b20",
      "service": "51.83.234.203:9999",
      "pubKeyOperator": "84008f643dfaf4470a9933e41f1f864a0309ec345726b4f443cb194c8a1318fdb224fe38297493d3d4932b4abcc43352",
      "valid": true,
      "pubKeyShare": "adfd7b96d740d73cdeb4d880e8c6fca78cab77f31c523fbffb53ba36598b376a267614be7f847e6f37a0e668d5d953ae"
    },
    {
      "proTxHash": "aa1d7efb277b44348b44d967beee3064c55c4f2deb8a35799fe823f8eee4bece",
      "service": "139.99.172.5:9999",
      "pubKeyOperator": "a83d2ac03722920d1e3280a921fa3db6579e7d9827a52ae451adcab997e3f63195534a92300b712a12bd6f3a8a284ec4",
      "valid": true,
      "pubKeyShare": "85749d855e4da673468a2114c04d27f0551fa78a684b72099b52b6cab6acdd631d51d3e5ff009433cd9f75bed9486c4a"
    },
    {
      "proTxHash": "092828edc081d3ba1a54f043259b941368f2c718881b06a9d347883b21bfe313",
      "service": "65.108.74.75:9999",
      "pubKeyOperator": "b1b38452732a88ee6949226aa693a7f96733facba4013764a38d46b831ec78d7f4bde1906e41c6869918b8f7931de5f4",
      "valid": true,
      "pubKeyShare": "aa65773499258cba4fda67bf7352d87079777bc800a35f9c551e141a355aa51c5d259f0296d72124a6bf715745f79899"
    },
    {
      "proTxHash": "c418f3897f923d8b5e1c5087b552e38cfde15292299ac4de0fc415be0ed08230",
      "service": "65.108.74.78:9999",
      "pubKeyOperator": "a2d3741656a40811fdee2cd488140935b7afb874730f8a2afad6493212607848f40410f9cd3175227acbee441b98fe85",
      "valid": true,
      "pubKeyShare": "81eda5411e9301bfe328bf70226cb9e8e5662f0d637d3e31b5e8ed09c07ccae2467d2f44cb6a94194ded70451443bd57"
    },
    {
      "proTxHash": "78fc0590dfa8f386b03d3d3a2e9e50b12c138997af115fc4c5347981ed70355d",
      "service": "65.21.147.225:9999",
      "pubKeyOperator": "8845a008aae558e5fdc45fa37bdcf84d0d8b067b387e4775ee07e0a2c7d246f44d5c86c592b17c709a046dbc8417cc0e",
      "valid": true,
      "pubKeyShare": "b899829aad9dd1aa87d68c955e9d7007c045ecaa057057147c92661f63226ae49d6f12b3812fadf7d4a2cf6402d2010f"
    },
    {
      "proTxHash": "e4bb242753bad98939b0174a1b3354455e57a944abe77e8cc208435ad68cb8a7",
      "service": "207.148.66.223:9999",
      "pubKeyOperator": "b2136fc6e7f341d933798b0f921936bf2f2f97036604eef84ba04564db4a247d7cedb10fde22f76a46f434c44fbafe4e",
      "valid": true,
      "pubKeyShare": "b18240ed0f39fdd5cba26b96316785263c17d91b1f04fcf4a5430187022490a130f6c0d041831e93d59cffdfcace59be"
    },
    {
      "proTxHash": "06a9ee248111bf6d6d5b123cc40b3a9c9c9c3c84a58e5a2ed9df97ad7c4e7289",
      "service": "5.189.186.78:9999",
      "pubKeyOperator": "8873348f84327aabe2920d571f51d4a39da2c8c5ac1315c9d0776f3e8af256504d5b52472c2e24bfe1aeba3572f230f4",
      "valid": true,
      "pubKeyShare": "affd76b839d008405462be12633804b8705953086cdc27ae1aab2d8d1b138bab14f6b60ec34381b90e735bfc64f4b8c4"
    },
    {
      "proTxHash": "43c94cb00d47132db6595cc88f08a4ac2af5e3f8faf9adc00a791f1e8cd70515",
      "service": "194.163.143.174:9999",
      "pubKeyOperator": "8269bdb3e3d6e0f18fa00ee0e99a3658fb1cff52f8b7c1988805610add9d1b1c950489fb4263097c69fa7569fd3e281f",
      "valid": true,
      "pubKeyShare": "a99de1fc6d6c2883a306ac1b08f94d768b2d372faeb6d12f51263838a258d664917c93aa785b64172730c27b1ab21576"
    },
    {
      "proTxHash": "7457a10b5a1ca0088d2c5d381d1e41dd736b05b6f44588e8f94a1592109ed12d",
      "service": "161.97.160.87:9999",
      "pubKeyOperator": "8a763cb66be8967b124cd055e3e19a2594315f81e6d978a4af3f63618f6f085ca2769175ec3054967656f1fac10762d5",
      "valid": true,
      "pubKeyShare": "a9f368d889ba3d2f3ada28d3b178e5e69f5897e831ce4a430c619888dca12e4e23c35c3906f5db3de25d2130b9d5f212"
    },
    {
      "proTxHash": "41c180ca7da99171c1c5e983d72172897d78190a68831410f9a786489e6d73bb",
      "service": "38.242.224.124:9999",
      "pubKeyOperator": "b2031c362f21d23094f81e71b6c7da4a05dda8067edf5a0abf484d33c7905b1cb781c74085954566f8ae6fc00cbc850a",
      "valid": true,
      "pubKeyShare": "85aafd4cf0232080dc1956169e0bf551d5428ab2b00a46a71d61aea5fa55f223b2fb5781b226f873012b492fa3fa0c78"
    },
    {
      "proTxHash": "f513574b70a029ef1f482cf31a7a338e7fb1a7b0337929a0dd3a480eb9faa4b3",
      "service": "46.254.241.9:9999",
      "pubKeyOperator": "b5985cb4fa9498fbfe0dfcd91f58d9e1e11a05938eec1f6a0623849901ca2c3dc75b489fb63cfac0b3b89701fa121945",
      "valid": true,
      "pubKeyShare": "86402fff569e41916899339bec2d29dfdf12744ae7cfbc1e69f7e64b4ae8592ecbd770e4ad7fc1f000563a350754e5d0"
    }
  ],
  "quorumPublicKey": "8f96647c29b3045e1877e795c3dc0b5b1e416dece18f14eec74619ca57f46fe06cf9b40aa43e8f30aa040f81cf0f19fb",
  "secretKeyShare": ""
}`
