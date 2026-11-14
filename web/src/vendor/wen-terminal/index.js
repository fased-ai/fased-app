import { Fragment as e, jsx as t, jsxs as n } from "react/jsx-runtime";
import { useEffect as r, useMemo as i, useRef as a, useState as o } from "react";
var s = -32700, c = -32603, l = -32602, u = -32601, d = -32600, f = -32016, p = -32015, m = -32014, h = -32013, g = -32012, _ = -32011, v = -32010, y = -32009, b = -32008, x = -32007, S = -32006, C = -32005, w = -32004, T = -32003, E = -32002, D = -32001, O = 28e5, k = 2800001, A = 2800002, ee = 2800003, j = 2800004, M = 2800005, N = 2800006, P = 2800007, F = 2800008, te = 2800009, I = 2800010, ne = 2800011, L = 323e4, re = 32300001, ie = 3230002, ae = 3230003, oe = 3230004, R = 361e4, z = 3610001, se = 3610002, ce = 3610003, B = 3610004, V = 3610005, le = 3610006, ue = 3610007, de = 3611e3, fe = 3704e3, pe = 3704001, me = 3704002, he = 3704003, ge = 3704004, _e = 4128e3, ve = 4128001, ye = 4128002, be = 4615e3, xe = 4615001, Se = 4615002, Ce = 4615003, we = 4615004, Te = 4615005, Ee = 4615006, De = 4615007, Oe = 4615008, ke = 4615009, Ae = 4615010, je = 4615011, Me = 4615012, Ne = 4615013, Pe = 4615014, Fe = 4615015, Ie = 4615016, Le = 4615017, Re = 4615018, ze = 4615019, Be = 4615020, Ve = 4615021, He = 4615022, Ue = 4615023, We = 4615024, Ge = 4615025, Ke = 4615026, qe = 4615027, Je = 4615028, Ye = 4615029, Xe = 4615030, Ze = 4615031, Qe = 4615032, $e = 4615033, et = 4615034, tt = 4615035, nt = 4615036, rt = 4615037, it = 4615038, at = 4615039, ot = 4615040, st = 4615041, ct = 4615042, lt = 4615043, ut = 4615044, dt = 4615045, ft = 4615046, pt = 4615047, mt = 4615048, ht = 4615049, gt = 4615050, _t = 4615051, vt = 4615052, yt = 4615053, bt = 4615054, xt = 5508e3, St = 5508001, Ct = 5508002, wt = 5508003, Tt = 5508004, Et = 5508005, Dt = 5508006, Ot = 5508007, kt = 5508008, At = 5508009, jt = 5508010, Mt = 5508011, Nt = 5663e3, Pt = 5663001, Ft = 5663002, It = 5663003, Lt = 5663004, Rt = 5663005, zt = 5663006, Bt = 5663007, Vt = 5663008, Ht = 5663009, Ut = 5663010, Wt = 5663011, Gt = 5663012, Kt = 5663013, qt = 5663014, Jt = 5663015, Yt = 5663016, Xt = 5663017, Zt = 5663018, Qt = 5663019, $t = 5663020, en = 705e4, tn = 7050001, nn = 7050002, rn = 7050003, an = 7050004, on = 7050005, sn = 7050006, cn = 7050007, ln = 7050008, un = 7050009, dn = 7050010, fn = 7050011, pn = 7050012, mn = 7050013, hn = 7050014, gn = 7050015, _n = 7050016, vn = 7050017, yn = 7050018, bn = 7050019, xn = 7050020, Sn = 7050021, Cn = 7050022, wn = 7050023, Tn = 7050024, En = 7050025, Dn = 7050026, On = 7050027, kn = 7050028, An = 7050029, jn = 7050030, Mn = 7050031, Nn = 7050032, Pn = 7050033, Fn = 7050034, In = 7050035, Ln = 7050036, Rn = 8078e3, zn = 8078001, Bn = 8078002, Vn = 8078003, Hn = 8078004, Un = 8078005, Wn = 8078006, Gn = 8078007, Kn = 8078008, qn = 8078009, Jn = 8078010, Yn = 8078011, Xn = 8078012, Zn = 8078013, Qn = 8078014, $n = 8078015, er = 8078016, tr = 8078017, nr = 8078018, rr = 8078019, ir = 8078020, ar = 8078021, or = 8078022, sr = 81e5, cr = 8100001, lr = 8100002, ur = 8100003, dr = 819e4, fr = 8190001, pr = 8190002, mr = 8190003, hr = 8190004, gr = 99e5, _r = 9900001, vr = 9900002, yr = 9900003, br = 9900004;
function xr(e) {
	return Array.isArray(e) ? "%5B" + e.map(xr).join("%2C%20") + "%5D" : typeof e == "bigint" ? `${e}n` : encodeURIComponent(String(e != null && Object.getPrototypeOf(e) === null ? { ...e } : e));
}
function Sr([e, t]) {
	return `${e}=${xr(t)}`;
}
function Cr(e) {
	let t = Object.entries(e).map(Sr).join("&");
	return btoa(t);
}
var wr = {
	[L]: "Account not found at address: $address",
	[oe]: "Not all accounts were decoded. Encoded accounts found at addresses: $addresses.",
	[ae]: "Expected decoded account at address: $address",
	[ie]: "Failed to decode account data at address: $address",
	[re]: "Accounts not found at addresses: $addresses",
	[te]: "Unable to find a viable program address bump seed.",
	[A]: "$putativeAddress is not a base58-encoded address.",
	[O]: "Expected base58 encoded address to decode to a byte array of length 32. Actual length: $actualLength.",
	[ee]: "The `CryptoKey` must be an `Ed25519` public key.",
	[ne]: "$putativeOffCurveAddress is not a base58-encoded off-curve address.",
	[F]: "Invalid seeds; point must fall off the Ed25519 curve.",
	[j]: "Expected given program derived address to have the following format: [Address, ProgramDerivedAddressBump].",
	[N]: "A maximum of $maxSeeds seeds, including the bump seed, may be supplied when creating an address. Received: $actual.",
	[P]: "The seed at index $index with length $actual exceeds the maximum length of $maxSeedLength bytes.",
	[M]: "Expected program derived address bump to be in the range [0, 255], got: $bump.",
	[I]: "Program address cannot end with PDA marker.",
	[k]: "Expected base58-encoded address string of length in the range [32, 44]. Actual length: $actualLength.",
	4: "Expected base58-encoded blockash string of length in the range [32, 44]. Actual length: $actualLength.",
	1: "The network has progressed past the last block for which this transaction could have been committed.",
	[Rn]: "Codec [$codecDescription] cannot decode empty byte arrays.",
	[or]: "Enum codec cannot use lexical values [$stringValues] as discriminators. Either remove all lexical values or set `useValuesAsDiscriminators` to `false`.",
	[ir]: "Sentinel [$hexSentinel] must not be present in encoded bytes [$hexEncodedBytes].",
	[Un]: "Encoder and decoder must have the same fixed size, got [$encoderFixedSize] and [$decoderFixedSize].",
	[Wn]: "Encoder and decoder must have the same max size, got [$encoderMaxSize] and [$decoderMaxSize].",
	[Hn]: "Encoder and decoder must either both be fixed-size or variable-size.",
	[Kn]: "Enum discriminator out of range. Expected a number in [$formattedValidDiscriminators], got $discriminator.",
	[Bn]: "Expected a fixed-size codec, got a variable-size one.",
	[Zn]: "Codec [$codecDescription] expected a positive byte length, got $bytesLength.",
	[Vn]: "Expected a variable-size codec, got a fixed-size one.",
	[rr]: "Codec [$codecDescription] expected zero-value [$hexZeroValue] to have the same size as the provided fixed-size item [$expectedSize bytes].",
	[zn]: "Codec [$codecDescription] expected $expected bytes, got $bytesLength.",
	[nr]: "Expected byte array constant [$hexConstant] to be present in data [$hexData] at offset [$offset].",
	[qn]: "Invalid discriminated union variant. Expected one of [$variants], got $value.",
	[Jn]: "Invalid enum variant. Expected one of [$stringValues] or a number in [$formattedNumericalValues], got $variant.",
	[$n]: "Invalid literal union variant. Expected one of [$variants], got $value.",
	[Gn]: "Expected [$codecDescription] to have $expected items, got $actual.",
	[Xn]: "Invalid value $value for base $base with alphabet $alphabet.",
	[er]: "Literal union discriminator out of range. Expected a number between $minRange and $maxRange, got $discriminator.",
	[Yn]: "Codec [$codecDescription] expected number to be in the range [$min, $max], got $value.",
	[Qn]: "Codec [$codecDescription] expected offset to be in the range [0, $bytesLength], got $offset.",
	[ar]: "Expected sentinel [$hexSentinel] to be present in decoded bytes [$hexDecodedBytes].",
	[tr]: "Union variant out of range. Expected an index between $minRange and $maxRange, got $variant.",
	[de]: "No random values implementation could be found.",
	[ke]: "instruction requires an uninitialized account",
	[Ue]: "instruction tries to borrow reference for an account which is already borrowed",
	[We]: "instruction left account with an outstanding borrowed reference",
	[Ve]: "program other than the account's owner changed the size of the account data",
	[Te]: "account data too small for instruction",
	[He]: "instruction expected an executable account",
	[ft]: "An account does not have enough lamports to be rent-exempt",
	[mt]: "Program arithmetic overflowed",
	[dt]: "Failed to serialize or deserialize account data: $encodedData",
	[bt]: "Builtin programs must consume compute units",
	[Qe]: "Cross-program invocation call depth too deep",
	[it]: "Computational budget exceeded",
	[Ke]: "custom program error: #$code",
	[Le]: "instruction contains duplicate accounts",
	[Ge]: "instruction modifications of multiply-passed account differ",
	[Xe]: "executable accounts must be rent exempt",
	[Je]: "instruction changed executable accounts data",
	[Ye]: "instruction changed the balance of an executable account",
	[Re]: "instruction changed executable bit of an account",
	[Pe]: "instruction modified data of an account it does not own",
	[Ne]: "instruction spent from the balance of an account it does not own",
	[xe]: "generic instruction error",
	[gt]: "Provided owner is not allowed",
	[lt]: "Account is immutable",
	[ut]: "Incorrect authority provided",
	[De]: "incorrect program id for instruction",
	[Ee]: "insufficient funds for instruction",
	[we]: "invalid account data for instruction",
	[pt]: "Invalid account owner",
	[Se]: "invalid program argument",
	[qe]: "program returned invalid error code",
	[Ce]: "invalid instruction data",
	[rt]: "Failed to reallocate account data",
	[nt]: "Provided seeds do not result in a valid address",
	[_t]: "Accounts data allocations exceeded the maximum allowed per transaction",
	[vt]: "Max accounts exceeded",
	[yt]: "Max instruction trace length exceeded",
	[tt]: "Length of the seed is too long for address generation",
	[$e]: "An account required by the instruction is missing",
	[Oe]: "missing required signature for instruction",
	[Me]: "instruction illegally modified the program id of an account",
	[Be]: "insufficient account keys for instruction",
	[at]: "Cross-program invocation with unauthorized signer or writable account",
	[ot]: "Failed to create program execution environment",
	[ct]: "Program failed to compile",
	[st]: "Program failed to complete",
	[Ie]: "instruction modified data of a read-only account",
	[Fe]: "instruction changed the balance of a read-only account",
	[et]: "Cross-program invocation reentrancy not allowed for this instruction",
	[ze]: "instruction modified rent epoch of an account",
	[je]: "sum of account balances before and after instruction do not match",
	[Ae]: "instruction requires an initialized account",
	[be]: "",
	[Ze]: "Unsupported program id",
	[ht]: "Unsupported sysvar",
	[_e]: "The instruction does not have any accounts.",
	[ve]: "The instruction does not have any data.",
	[ye]: "Expected instruction to have progress address $expectedProgramAddress, got $actualProgramAddress.",
	5: "Expected base58 encoded blockhash to decode to a byte array of length 32. Actual length: $actualLength.",
	2: "The nonce `$expectedNonceValue` is no longer valid. It has advanced to `$actualNonceValue`",
	[vr]: "Invariant violation: Found no abortable iterable cache entry for key `$cacheKey`. It should be impossible to hit this error; please file an issue at https://sola.na/web3invariant",
	[br]: "Invariant violation: This data publisher does not publish to the channel named `$channelName`. Supported channels include $supportedChannelNames.",
	[_r]: "Invariant violation: WebSocket message iterator state is corrupt; iterated without first resolving existing message promise. It should be impossible to hit this error; please file an issue at https://sola.na/web3invariant",
	[gr]: "Invariant violation: WebSocket message iterator is missing state storage. It should be impossible to hit this error; please file an issue at https://sola.na/web3invariant",
	[yr]: "Invariant violation: Switch statement non-exhaustive. Received unexpected value `$unexpectedValue`. It should be impossible to hit this error; please file an issue at https://sola.na/web3invariant",
	[c]: "JSON-RPC error: Internal JSON-RPC error ($__serverMessage)",
	[l]: "JSON-RPC error: Invalid method parameter(s) ($__serverMessage)",
	[d]: "JSON-RPC error: The JSON sent is not a valid `Request` object ($__serverMessage)",
	[u]: "JSON-RPC error: The method does not exist / is not available ($__serverMessage)",
	[s]: "JSON-RPC error: An error occurred on the server while parsing the JSON text ($__serverMessage)",
	[g]: "$__serverMessage",
	[D]: "$__serverMessage",
	[w]: "$__serverMessage",
	[m]: "$__serverMessage",
	[v]: "$__serverMessage",
	[y]: "$__serverMessage",
	[f]: "Minimum context slot has not been reached",
	[C]: "Node is unhealthy; behind by $numSlotsBehind slots",
	[b]: "No snapshot",
	[E]: "Transaction simulation failed",
	[x]: "$__serverMessage",
	[_]: "Transaction history is not available from this node",
	[S]: "$__serverMessage",
	[h]: "Transaction signature length mismatch",
	[T]: "Transaction signature verification failure",
	[p]: "$__serverMessage",
	[fe]: "Key pair bytes must be of length 64, got $byteLength.",
	[pe]: "Expected private key bytes with length 32. Actual length: $actualLength.",
	[me]: "Expected base58-encoded signature to decode to a byte array of length 64. Actual length: $actualLength.",
	[ge]: "The provided private key does not match the provided public key.",
	[he]: "Expected base58-encoded signature string of length in the range [64, 88]. Actual length: $actualLength.",
	6: "Lamports value must be in the range [0, 2e64-1]",
	7: "`$value` cannot be parsed as a `BigInt`",
	10: "$message",
	8: "`$value` cannot be parsed as a `Number`",
	3: "No nonce account could be found at address `$nonceAccountAddress`",
	[dr]: "The notification name must end in 'Notifications' and the API must supply a subscription plan creator function for the notification '$notificationName'.",
	[pr]: "WebSocket was closed before payload could be added to the send buffer",
	[mr]: "WebSocket connection closed",
	[hr]: "WebSocket failed to connect",
	[fr]: "Failed to obtain a subscription id from the server",
	[ur]: "Could not find an API plan for RPC method: `$method`",
	[sr]: "The $argumentLabel argument to the `$methodName` RPC method$optionalPathLabel was `$value`. This number is unsafe for use with the Solana JSON-RPC because it exceeds `Number.MAX_SAFE_INTEGER`.",
	[lr]: "HTTP error ($statusCode): $message",
	[cr]: "HTTP header(s) forbidden: $headers. Learn more at https://developer.mozilla.org/en-US/docs/Glossary/Forbidden_header_name.",
	[xt]: "Multiple distinct signers were identified for address `$address`. Please ensure that you are using the same signer instance for each address.",
	[St]: "The provided value does not implement the `KeyPairSigner` interface",
	[wt]: "The provided value does not implement the `MessageModifyingSigner` interface",
	[Tt]: "The provided value does not implement the `MessagePartialSigner` interface",
	[Ct]: "The provided value does not implement any of the `MessageSigner` interfaces",
	[Dt]: "The provided value does not implement the `TransactionModifyingSigner` interface",
	[Ot]: "The provided value does not implement the `TransactionPartialSigner` interface",
	[kt]: "The provided value does not implement the `TransactionSendingSigner` interface",
	[Et]: "The provided value does not implement any of the `TransactionSigner` interfaces",
	[At]: "More than one `TransactionSendingSigner` was identified.",
	[jt]: "No `TransactionSendingSigner` was identified. Please provide a valid `TransactionWithSingleSendingSigner` transaction.",
	[Mt]: "Wallet account signers do not support signing multiple messages/transactions in a single operation",
	[ue]: "Cannot export a non-extractable key.",
	[z]: "No digest implementation could be found.",
	[R]: "Cryptographic operations are only allowed in secure browser contexts. Read more here: https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts.",
	[se]: "This runtime does not support the generation of Ed25519 key pairs.\n\nInstall @solana/webcrypto-ed25519-polyfill and call its `install` function before generating keys in environments that do not support Ed25519.\n\nFor a list of runtimes that currently support Ed25519 operations, visit https://github.com/WICG/webcrypto-secure-curves/issues/20.",
	[ce]: "No signature verification implementation could be found.",
	[B]: "No key generation implementation could be found.",
	[V]: "No signing implementation could be found.",
	[le]: "No key export implementation could be found.",
	9: "Timestamp value must be in the range [-(2n ** 63n), (2n ** 63n) - 1]. `$value` given",
	[_n]: "Transaction processing left an account with an outstanding borrowed reference",
	[tn]: "Account in use",
	[nn]: "Account loaded twice",
	[rn]: "Attempt to debit an account but found no record of a prior credit.",
	[wn]: "Transaction loads an address table account that doesn't exist",
	[cn]: "This transaction has already been processed",
	[ln]: "Blockhash not found",
	[un]: "Loader call chain is too deep",
	[gn]: "Transactions are currently disabled due to cluster maintenance",
	[jn]: "Transaction contains a duplicate instruction ($index) that is not allowed",
	[on]: "Insufficient funds for fee",
	[Mn]: "Transaction results in an account ($accountIndex) with insufficient funds for rent",
	[sn]: "This account may not be used to pay transaction fees",
	[fn]: "Transaction contains an invalid account reference",
	[En]: "Transaction loads an address table account with invalid data",
	[Dn]: "Transaction address table lookup uses an invalid index",
	[Tn]: "Transaction loads an address table account with an invalid owner",
	[Pn]: "LoadedAccountsDataSizeLimit set for transaction must be greater than 0.",
	[mn]: "This program may not be used for executing instructions",
	[On]: "Transaction leaves an account with a lower balance than rent-exempt minimum",
	[bn]: "Transaction loads a writable account that cannot be written",
	[Nn]: "Transaction exceeded max loaded accounts data size cap",
	[dn]: "Transaction requires a fee but has no signature present",
	[an]: "Attempt to load a program that does not exist",
	[In]: "Execution of the program referenced by account at index $accountIndex is temporarily restricted.",
	[Fn]: "ResanitizationNeeded",
	[hn]: "Transaction failed to sanitize accounts offsets correctly",
	[pn]: "Transaction did not pass signature verification",
	[Cn]: "Transaction locked too many accounts",
	[Ln]: "Sum of account balances before and after transaction do not match",
	[en]: "The transaction failed with the error `$errorName`",
	[yn]: "Transaction version is unsupported",
	[Sn]: "Transaction would exceed account data limit within the block",
	[An]: "Transaction would exceed total account data limit",
	[xn]: "Transaction would exceed max account limit within the block",
	[vn]: "Transaction would exceed max Block Cost Limit",
	[kn]: "Transaction would exceed max Vote Cost Limit",
	[Jt]: "Attempted to sign a transaction with an address that is not a signer for it",
	[Ut]: "Transaction is missing an address at index: $index.",
	[Yt]: "Transaction has no expected signers therefore it cannot be encoded",
	[$t]: "Transaction size $transactionSize exceeds limit of $transactionSizeLimit bytes",
	[Ft]: "Transaction does not have a blockhash lifetime",
	[It]: "Transaction is not a durable nonce transaction",
	[Rt]: "Contents of these address lookup tables unknown: $lookupTableAddresses",
	[zt]: "Lookup of address at index $highestRequestedIndex failed for lookup table `$lookupTableAddress`. Highest known index is $highestKnownIndex. The lookup table may have been extended since its contents were retrieved",
	[Vt]: "No fee payer set in CompiledTransaction",
	[Bt]: "Could not find program address at index $index",
	[Zt]: "Failed to estimate the compute unit consumption for this transaction message. This is likely because simulating the transaction failed. Inspect the `cause` property of this error to learn more",
	[Qt]: "Transaction failed when it was simulated in order to estimate the compute unit consumption. The compute unit estimate provided is for a transaction that failed when simulated and may not be representative of the compute units this transaction would consume if successful. Inspect the `cause` property of this error to learn more",
	[Wt]: "Transaction is missing a fee payer.",
	[Gt]: "Could not determine this transaction's signature. Make sure that the transaction has been signed by its fee payer.",
	[qt]: "Transaction first instruction is not advance nonce account instruction.",
	[Kt]: "Transaction with no instructions cannot be durable nonce transaction.",
	[Nt]: "This transaction includes an address (`$programAddress`) which is both invoked and set as the fee payer. Program addresses may not pay fees",
	[Pt]: "This transaction includes an address (`$programAddress`) which is both invoked and marked writable. Program addresses may not be writable",
	[Xt]: "The transaction message expected the transaction to have $signerAddressesLength signatures, got $signaturesLength.",
	[Ht]: "Transaction is missing signatures for addresses: $addresses.",
	[Lt]: "Transaction version must be in the range [0, 127]. `$actualVersion` given"
}, H = "i", U = "t";
function Tr(e, t = {}) {
	let n = wr[e];
	if (n.length === 0) return "";
	let r;
	function i(e) {
		if (r[U] === 2) {
			let i = n.slice(r[H] + 1, e);
			a.push(i in t ? `${t[i]}` : `$${i}`);
		} else r[U] === 1 && a.push(n.slice(r[H], e));
	}
	let a = [];
	return n.split("").forEach((e, t) => {
		if (t === 0) {
			r = {
				[H]: 0,
				[U]: n[0] === "\\" ? 0 : n[0] === "$" ? 2 : 1
			};
			return;
		}
		let a;
		switch (r[U]) {
			case 0:
				a = {
					[H]: t,
					[U]: 1
				};
				break;
			case 1:
				e === "\\" ? a = {
					[H]: t,
					[U]: 0
				} : e === "$" && (a = {
					[H]: t,
					[U]: 2
				});
				break;
			case 2: e === "\\" ? a = {
				[H]: t,
				[U]: 0
			} : e === "$" ? a = {
				[H]: t,
				[U]: 2
			} : e.match(/\w/) || (a = {
				[H]: t,
				[U]: 1
			});
		}
		a && (r !== a && i(t), r = a);
	}), i(), a.join("");
}
function Er(e, t = {}) {
	if (process.env.NODE_ENV !== "production") return Tr(e, t);
	{
		let n = `Solana error #${e}; Decode this error by running \`npx @solana/errors decode -- ${e}`;
		return Object.keys(t).length && (n += ` '${Cr(t)}'`), `${n}\``;
	}
}
var Dr = class extends Error {
	cause = this.cause;
	context;
	constructor(...[e, t]) {
		let n, r;
		if (t) {
			let { cause: e, ...i } = t;
			e && (r = { cause: e }), Object.keys(i).length > 0 && (n = i);
		}
		let i = Er(e, n);
		super(i, r), this.context = {
			__code: e,
			...n
		}, this.name = "SolanaError";
	}
};
//#endregion
//#region ../../node_modules/@solana/codecs-core/dist/index.browser.mjs
function Or(e, t) {
	return "fixedSize" in t ? t.fixedSize : t.getSizeFromValue(e);
}
function kr(e) {
	return Object.freeze({
		...e,
		encode: (t) => {
			let n = new Uint8Array(Or(t, e));
			return e.write(t, n, 0), n;
		}
	});
}
function Ar(e) {
	return "fixedSize" in e && typeof e.fixedSize == "number";
}
function jr(e) {
	return !Ar(e);
}
function Mr(e, t) {
	return kr({
		fixedSize: t,
		write: (n, r, i) => {
			let a = e.encode(n), o = a.length > t ? a.slice(0, t) : a;
			return r.set(o, i), i + t;
		}
	});
}
function Nr(e, t) {
	return kr({
		...jr(e) ? {
			...e,
			getSizeFromValue: (n) => e.getSizeFromValue(t(n))
		} : e,
		write: (n, r, i) => e.write(t(n), r, i)
	});
}
//#endregion
//#region ../../node_modules/@solana/codecs-strings/dist/index.browser.mjs
function Pr(e, t, n = t) {
	if (!t.match(RegExp(`^[${e}]*$`))) throw new Dr(Xn, {
		alphabet: e,
		base: e.length,
		value: n
	});
}
var Fr = (e) => kr({
	getSizeFromValue: (t) => {
		let [n, r] = Ir(t, e[0]);
		if (!r) return t.length;
		let i = Lr(r, e);
		return n.length + Math.ceil(i.toString(16).length / 2);
	},
	write(t, n, r) {
		if (Pr(e, t), t === "") return r;
		let [i, a] = Ir(t, e[0]);
		if (!a) return n.set(new Uint8Array(i.length).fill(0), r), r + i.length;
		let o = Lr(a, e), s = [];
		for (; o > 0n;) s.unshift(Number(o % 256n)), o /= 256n;
		let c = [...Array(i.length).fill(0), ...s];
		return n.set(c, r), r + c.length;
	}
});
function Ir(e, t) {
	let [n, r] = e.split(RegExp(`((?!${t}).*)`));
	return [n, r];
}
function Lr(e, t) {
	let n = BigInt(t.length), r = 0n;
	for (let i of e) r *= n, r += BigInt(t.indexOf(i));
	return r;
}
var Rr = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", zr = () => Fr(Rr);
globalThis.TextDecoder, globalThis.TextEncoder;
//#endregion
//#region ../../node_modules/@solana/addresses/dist/index.browser.mjs
var Br;
function Vr() {
	return Br ||= zr(), Br;
}
function Hr(e) {
	if (e.length < 32 || e.length > 44) throw new Dr(k, { actualLength: e.length });
	let t = Vr().encode(e).byteLength;
	if (t !== 32) throw new Dr(O, { actualLength: t });
}
function Ur(e) {
	return Hr(e), e;
}
function Wr() {
	return Nr(Mr(Vr(), 32), (e) => Ur(e));
}
//#endregion
//#region ../../src/mining-read-client.mjs
var Gr = "/__wen/mining-read", W = (e, t) => {
	if (!e) throw Error(t);
}, Kr = (e) => Array.isArray(e) ? e.map(Kr) : e && typeof e == "object" ? Object.fromEntries(Object.keys(e).sort().map((t) => [t, Kr(e[t])])) : e, G = (e, t) => JSON.stringify(Kr(e)) === JSON.stringify(Kr(t)), K = (e) => typeof e == "string" && /^(0|[1-9]\d*)$/.test(e) && BigInt(e) < 1n << 64n, qr = (e) => Array.from(Wr().encode(e), (e) => e.toString(16).padStart(2, "0")).join("");
function Jr({ fetcher: e = globalThis.fetch, now: t = Date.now } = {}) {
	let n = null;
	async function r(t, n, r) {
		let i = new AbortController(), a = () => i.abort();
		r?.aborted && a(), r?.addEventListener("abort", a, { once: !0 });
		let o = setTimeout(a, 15e3);
		try {
			let r = await e(t, {
				method: n ? "POST" : "GET",
				...n ? {
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify(n)
				} : {},
				signal: i.signal,
				credentials: "omit",
				cache: "no-store"
			});
			W(r.ok, "Read unavailable. Refresh this wallet and round.");
			let a = r.body?.getReader();
			W(a, "Missing read response");
			let o = 0, s = [];
			try {
				for (;;) {
					let { value: e, done: t } = await a.read();
					if (t) break;
					o += e.byteLength, W(o <= 2097152, "Read response too large"), s.push(e);
				}
			} finally {
				await a.cancel();
			}
			let c = new Uint8Array(o), l = 0;
			for (let e of s) c.set(e, l), l += e.length;
			return JSON.parse(new TextDecoder().decode(c));
		} finally {
			clearTimeout(o), r?.removeEventListener("abort", a);
		}
	}
	function i(e) {
		W(/^[0-9a-f]{64}$/.test(e.wallet) && !/^0+$/.test(e.wallet) && K(e.roundSlot), "Invalid wallet or round"), W(e.includeBtc === void 0 || typeof e.includeBtc == "boolean", "Invalid BTC scope"), W(e.includeStaking === void 0 || typeof e.includeStaking == "boolean", "Invalid staking scope"), W(e.includeOperating === void 0 || typeof e.includeOperating == "boolean", "Invalid Operating scope"), W(e.includeTreasury === void 0 || typeof e.includeTreasury == "boolean", "Invalid treasury scope"), W(e.includeDaily === void 0 || typeof e.includeDaily == "boolean", "Invalid daily scope"), W(e.includeCarry === void 0 || typeof e.includeCarry == "boolean", "Invalid carry scope");
	}
	async function a(e) {
		let t = await r(Gr + "/context", null, e);
		return W(t?.schema === "wen-mining-read-context/2" && t.execution === "disabled" && ["rpc-verified", "local-rpc-fixture"].includes(t.source) && /^[0-9a-f]{64}$/.test(t.revision), "Invalid deployment context"), W(t.identity?.binding && [
			"program",
			"ledger",
			"network",
			"economy",
			"terms"
		].every((e) => /^[0-9a-f]{64}$/.test(t.identity.binding[e])) && /^[0-9a-f]{64}$/.test(t.identity.assetMint) && /^[0-9a-f]{64}$/.test(t.identity.runtimeAddress), "Invalid deployment identity"), n ? W(G(t, n), "Deployment context changed. Reopen this read service.") : n = structuredClone(t), t;
	}
	function o(e, n) {
		W(e?.execution === "disabled" && e.source === n.source && G(e.identity, n.identity), "Wrong deployment response"), W(Number.isSafeInteger(e.observedAtMs) && Number.isSafeInteger(e.expiresAtMs) && e.observedAtMs <= t() + 1e4 && e.expiresAtMs > t() && e.expiresAtMs - e.observedAtMs > 0 && e.expiresAtMs - e.observedAtMs <= 12e4, "Stale read response");
	}
	function s(e, t, n) {
		o(e, t), W(e.schema === "wen-mining-deployment-read/2" && e.trust === "pinned-finalized-rpc-not-state-proof" && K(e.slot) && e.coverage?.kind === "settled-round" && e.coverage.complete === !0 && e.coverage.walletHistoryComplete === !1 && e.coverage.wallet === n.wallet && e.coverage.roundSlot === n.roundSlot, "Incomplete or wrong read scope");
		let r = e.projection;
		if (W(G(r?.binding, t.identity.binding) && r.assetMint === t.identity.assetMint && r.runtimeAddress === t.identity.runtimeAddress && Array.isArray(r.entries) && r.entries.every((e) => e.owner === n.wallet && e.slot === n.roundSlot && [
			"ordinal",
			"committedLamports",
			"principalEntitlementLamports",
			"principalReturnedLamports",
			"stakingRefundLamports",
			"stakingRefundReturnedLamports",
			"satAwardedAtoms",
			"satClaimedAtoms"
		].every((t) => K(e[t]))), "Wrong wallet projection"), n.includeBtc === !0 ? (W(t.identity.payout && e.coverage.includedFamilies?.includes("mining-payout") && e.btc && G(e.btc.pins, t.identity.payout), "Missing authenticated BTC scope"), W(e.btc.state?.value && typeof e.btc.state.value.pending == "boolean" && [
			"allocation-pending",
			"recovering",
			"ready",
			"awaiting-vaults"
		].includes(e.btc.status) && e.btc.status === "allocation-pending" === e.btc.state.value.pending && K(e.btc.unallocatedSpentLamports) && K(e.btc.unallocatedBtcAtoms), "Invalid BTC allocation state"), W(Array.isArray(r.payouts) && r.payouts.every((e) => e.owner === n.wallet && e.slot === n.roundSlot && [
			"ordinal",
			"inputLamports",
			"spentLamports",
			"pendingConversionLamports",
			"btcAcquiredAtoms",
			"btcPaidAtoms",
			"btcUnclaimedAtoms",
			"recoveredLamports"
		].every((t) => K(e[t]))), "Wrong BTC wallet projection")) : W(!e.coverage.includedFamilies?.includes("mining-payout") && !e.btc, "Unexpected BTC scope"), n.includeStaking === !0) {
			let r = e.staking;
			W(t.identity.payout && e.coverage.includedFamilies?.includes("staking-rewards") && r && Array.isArray(r.rows), "Missing authenticated staking scope"), W([
				"no-eligible-stakers",
				"bootstrap-no-staking",
				"no-staking-allocation",
				"allocation-pending",
				"recovering",
				"ready",
				"awaiting-vaults"
			].includes(r.status), "Invalid staking status"), W(r.rows.every((e) => e.owner === n.wallet && e.slot === n.roundSlot && [
				"ordinal",
				"inputLamports",
				"spentLamports",
				"pendingConversionLamports",
				"btcAcquiredAtoms",
				"btcPaidAtoms",
				"btcUnclaimedAtoms",
				"recoveredLamports"
			].every((t) => K(e[t])) && typeof e.recoveryClaimed == "boolean"), "Wrong staking beneficiary"), [
				"no-eligible-stakers",
				"bootstrap-no-staking",
				"no-staking-allocation"
			].includes(r.status) ? W(r.rows.length === 0 && r.cohort === null, "Unexpected zero-stake rewards") : W(r.cohort?.value?.slot === n.roundSlot && typeof r.cohort.value.pending == "boolean" && r.status === "allocation-pending" === r.cohort.value.pending && G(r.pins, t.identity.payout) && K(r.unallocatedSpentLamports) && K(r.unallocatedBtcAtoms), "Wrong staking cohort"), W(new Set(r.rows.map((e) => e.ordinal)).size === r.rows.length, "Duplicate staking beneficiary");
		} else W(!e.coverage.includedFamilies?.includes("staking-rewards") && !e.staking, "Unexpected staking scope");
		if (n.includeCarry === !0) {
			let r = e.carry;
			W(t.identity.payout && e.coverage.includedFamilies?.includes("carry") && r && G(r.pins, t.identity.payout) && Array.isArray(r.releaseRows) && Array.isArray(r.refundRows), "Missing authenticated carry scope"), W([
				"controller-unavailable",
				"decision-unavailable",
				"collecting",
				"ready",
				"release-allocation-pending",
				"refundable",
				"refund-allocation-pending"
			].includes(r.status) && typeof r.allocationComplete == "boolean", "Invalid carry allocation status");
			for (let e of [r.releaseRows, r.refundRows]) W(e.every((e) => e.owner === n.wallet && e.slot === n.roundSlot && [
				"ordinal",
				"btcEntitlementAtoms",
				"btcPaidAtoms",
				"solEntitlementLamports",
				"solPaidLamports"
			].every((t) => K(e[t])) && BigInt(e.btcPaidAtoms) <= BigInt(e.btcEntitlementAtoms) && BigInt(e.solPaidLamports) <= BigInt(e.solEntitlementLamports)) && new Set(e.map((e) => e.ordinal)).size === e.length, "Wrong carry beneficiary");
		} else W(!e.coverage.includedFamilies?.includes("carry") && !e.carry, "Unexpected carry scope");
		if (n.includeDaily === !0) {
			let i = e.daily;
			W(t.identity.payout && e.coverage.includedFamilies?.includes("daily") && i && G(i.pins, t.identity.payout) && K(i.dayId) && Array.isArray(i.prizeRows) && i.prizeRows.length <= 1 && Array.isArray(i.refundRows), "Missing authenticated daily scope"), W([
				"controller-unavailable",
				"day-unavailable",
				"scoring-pending",
				"reserving",
				"review",
				"correcting",
				"collecting",
				"allocating",
				"finalized",
				"refund-allocation-pending",
				"refundable"
			].includes(i.status) && typeof i.allocationComplete == "boolean", "Invalid daily allocation status");
			for (let e of [i.prizeRows, i.refundRows]) W(e.every((e) => e.owner === n.wallet && e.slot === n.roundSlot && [
				"ordinal",
				"btcEntitlementAtoms",
				"btcPaidAtoms",
				"solEntitlementLamports",
				"solPaidLamports"
			].every((t) => K(e[t])) && BigInt(e.btcPaidAtoms) <= BigInt(e.btcEntitlementAtoms) && BigInt(e.solPaidLamports) <= BigInt(e.solEntitlementLamports) && typeof e.btcClaimed == "boolean" && typeof e.solClaimed == "boolean" && e.btcPaidAtoms === (e.btcClaimed ? e.btcEntitlementAtoms : "0") && e.solPaidLamports === (e.solClaimed ? e.solEntitlementLamports : "0") && (!e.btcClaimed || BigInt(e.btcEntitlementAtoms) > 0n) && (!e.solClaimed || BigInt(e.solEntitlementLamports) > 0n)) && new Set(e.map((e) => e.ordinal)).size === e.length, "Wrong daily beneficiary");
			let a = r.records?.find((e) => e.family === "mining-execution" && e.type === "Round" && e.value?.slot === n.roundSlot)?.value;
			W(a && K(a.start) && String(BigInt(a.start) / 86400n) === i.dayId, "Wrong daily source day"), W(i.prizeRows.every((e) => e.dayId === i.dayId && K(e.contestId) && e.memberOrdinal === e.ordinal), "Wrong daily award identity");
		} else W(!e.coverage.includedFamilies?.includes("daily") && !e.daily, "Unexpected daily scope");
		if (n.includeTreasury === !0) {
			let i = e.treasury, a = t.identity.treasury;
			W(a && e.coverage.includedFamilies?.includes("mining-treasury") && i && G(i.pins, a) && i.scope === "selected-day-and-pending-order" && [
				"ready",
				"paused",
				"order-pending"
			].includes(i.status) && K(i.dayId) && i.state?.address === a.stateAddress && i.coverage?.historicalFundingComplete === !1, "Missing authenticated treasury scope");
			let o = i.state.value;
			W(o && [
				"liquidity_sol",
				"liquidity_wrapped",
				"locked_sat",
				"liquidity_sat",
				"owned_usdc",
				"acquired_btc"
			].every((e) => K(o[e])) && ["source_sol", "reserved"].every((e) => Array.isArray(o[e]) && o[e].length === 2 && o[e].every(K)) && BigInt(o.liquidity_wrapped) <= BigInt(o.liquidity_sol), "Invalid treasury accounted amounts");
			let s = r.records?.find((e) => e.family === "mining-execution" && e.type === "Round" && e.value?.slot === n.roundSlot)?.value;
			W(s && K(s.start) && String(BigInt(s.start) / 86400n) === i.dayId, "Wrong treasury budget day"), W(Array.isArray(i.vaults) && i.vaults.length <= 7 && new Set(i.vaults.map((e) => e.kind)).size === i.vaults.length && i.vaults.every((e) => Number.isInteger(e.kind) && e.kind >= 0 && e.kind < 7 && e.owner === a.stateAddress && ["address", "mint"].every((t) => /^[0-9a-f]{64}$/.test(e[t])) && [
				"amountAtoms",
				"ownedAtoms",
				"excessAtoms"
			].every((t) => K(e[t])) && BigInt(e.amountAtoms) === BigInt(e.ownedAtoms) + BigInt(e.excessAtoms)), "Invalid treasury custody"), W(Array.isArray(i.orders) && Array.isArray(i.budgets) && Array.isArray(i.conversions) && new Set(i.orders.map((e) => e.address)).size === i.orders.length && i.orders.every((e) => /^[0-9a-f]{64}$/.test(e.address) && K(e.value?.day) && Number.isInteger(e.value.hour) && e.value.hour >= 1 && e.value.hour < 24 && [0, 1].includes(e.value.side) && [
				0,
				1,
				2
			].includes(e.value.phase) && [
				"input",
				"acquired",
				"sol_deposited",
				"sat_deposited",
				"lp_burned"
			].every((t) => K(e.value[t])) && Array.isArray(e.attempts) && e.attempts.length === e.value.attempts && e.attempts.every((t, n) => t.value.day === e.value.day && t.value.hour === e.value.hour && t.value.number === n + 1)), "Invalid treasury order census");
		} else W(!e.coverage.includedFamilies?.includes("mining-treasury") && !e.treasury, "Unexpected treasury scope");
		if (n.includeOperating === !0) {
			let r = e.operating, i = t.identity.operating, a = r?.selection, o = r?.state?.value, s = r?.custody;
			if (W(i && r && G(r.pins, i) && e.coverage.includedFamilies?.includes("operating") && r.state.address === i.stateAddress && o.recipient === i.recipient && r.coverage?.scope === "selected-page-collected-rounds" && r.coverage.historicalCollectionsComplete === !1 && r.coverage.costHistoryComplete === !1, "Missing authenticated Operating scope"), W(a && a.roundSlot === n.roundSlot && a.roundAddress === e.coverage.roundAddress && K(a.pageIndex) && BigInt(a.pageIndex) === BigInt(n.roundSlot) / 64n && typeof a.eligible == "boolean" && typeof a.collected == "boolean" && (!a.collected || a.eligible) && [
				"amountLamports",
				"winnerFeesLamports",
				"forfeitureIncomeLamports"
			].every((e) => K(a[e])) && BigInt(a.amountLamports) === BigInt(a.winnerFeesLamports) + BigInt(a.forfeitureIncomeLamports), "Invalid Operating source"), W([
				"rounds",
				"total_collected",
				"winner_fees",
				"forfeiture_income"
			].every((e) => K(o[e])) && BigInt(o.total_collected) === BigInt(o.winner_fees) + BigInt(o.forfeiture_income) && s?.runtimeAddress === t.identity.runtimeAddress && [
				"runtimeOperatingAccruedLamports",
				"totalCollectedLamports",
				"uncollectedOperatingLamports",
				"runtimeBalanceLamports"
			].every((e) => K(s[e])) && s.totalCollectedLamports === o.total_collected && BigInt(s.runtimeOperatingAccruedLamports) === BigInt(s.totalCollectedLamports) + BigInt(s.uncollectedOperatingLamports), "Invalid Operating accounting"), W(Array.isArray(r.sources) && r.sources.length <= 64 && new Set(r.sources.map((e) => e.address)).size === r.sources.length && r.sources.every((e) => /^[0-9a-f]{64}$/.test(e.address) && K(e.value?.slot) && BigInt(e.value.slot) / 64n === BigInt(a.pageIndex) && [
				"amountLamports",
				"winnerFeesLamports",
				"forfeitureIncomeLamports"
			].every((t) => K(e[t])) && BigInt(e.amountLamports) === BigInt(e.winnerFeesLamports) + BigInt(e.forfeitureIncomeLamports)), "Invalid Operating collected sources"), r.page) {
				let e = r.page.value;
				W(r.page.address === a.pageAddress && e.page === a.pageIndex && [
					"collected",
					"total_collected",
					"winner_fees",
					"forfeiture_income"
				].every((t) => K(e[t])) && a.collected === ((BigInt(e.collected) & 1n << BigInt(n.roundSlot) % 64n) != 0n), "Wrong Operating page"), W(r.sources.reduce((e, t) => e | 1n << BigInt(t.value.slot) % 64n, 0n) === BigInt(e.collected) && r.sources.reduce((e, t) => e + BigInt(t.amountLamports), 0n) === BigInt(e.total_collected), "Incomplete Operating page");
			} else W(!a.collected && r.sources.length === 0, "Collection without Operating page");
		} else W(!e.coverage.includedFamilies?.includes("operating") && !e.operating, "Unexpected Operating scope");
	}
	return {
		walletHex: qr,
		async read(e, t) {
			e = structuredClone(e), i(e);
			let n = await a(t), o = await r(Gr, e, t);
			return s(o, n, e), o;
		},
		async history(e, t) {
			e = structuredClone(e), i({
				...e,
				roundSlot: "0"
			}), W(typeof e.includeBtc == "boolean" && (e.cursor === null || typeof e.cursor == "string"), "Invalid history selection");
			let n = await a(t), c = await r(Gr + "/history", e, t);
			o(c, n), W(c.schema === "wen-mining-history-read/2" && c.trust === "pinned-finalized-rpc-not-state-proof" && c.wallet === e.wallet && c.includeBtc === e.includeBtc && c.includeStaking === !0 == (e.includeStaking === !0) && c.includeCarry === !0 == (e.includeCarry === !0) && c.includeDaily === !0 == (e.includeDaily === !0) && c.includeTreasury === !0 == (e.includeTreasury === !0) && c.includeOperating === !0 == (e.includeOperating === !0) && /^[0-9a-f]{64}$/.test(c.manifestId), "Wrong history scope"), W(c.coverage?.kind === "indexed-rounds" && c.coverage.indexComplete === !1 && c.coverage.walletHistoryComplete === !1 && c.coverage.atomic === !1, "Unsupported history completeness"), W(Number.isSafeInteger(c.totalRounds) && c.totalRounds >= 0 && c.totalRounds <= 128 && Number.isSafeInteger(c.offset) && c.offset >= 0 && c.offset <= c.totalRounds && Array.isArray(c.roundSlots) && c.roundSlots.length <= Math.min(2, c.totalRounds - c.offset) && (c.roundSlots.length > 0 || c.totalRounds === 0) && c.roundSlots.every(K) && c.roundSlots.every((e, t) => !t || BigInt(e) < BigInt(c.roundSlots[t - 1])) && Array.isArray(c.items) && c.items.length === c.roundSlots.length, "Incomplete history page");
			let l = (t) => {
				W(typeof t == "string" && t.length <= 1024 && /^[A-Za-z0-9_-]+$/.test(t), "Invalid history cursor");
				let n = JSON.parse(atob(t.replace(/-/g, "+").replace(/_/g, "/")));
				return W(n.manifestId === c.manifestId && n.wallet === e.wallet && n.includeBtc === e.includeBtc && n.includeStaking === !0 == (e.includeStaking === !0) && n.includeCarry === !0 == (e.includeCarry === !0) && n.includeDaily === !0 == (e.includeDaily === !0) && n.includeTreasury === !0 == (e.includeTreasury === !0) && n.includeOperating === !0 == (e.includeOperating === !0) && Number.isSafeInteger(n.offset), "History cursor changed"), n;
			};
			W(e.cursor === null ? c.offset === 0 : l(e.cursor).offset === c.offset, "Wrong history offset");
			let u = c.offset + c.items.length;
			return W(u < c.totalRounds ? l(c.nextCursor).offset === u : c.nextCursor === null, "Wrong next cursor"), c.items.forEach((t, r) => {
				W(t.roundSlot === c.roundSlots[r], "Wrong indexed round"), t.status === "verified" ? s(t.snapshot, n, {
					wallet: e.wallet,
					roundSlot: t.roundSlot,
					includeBtc: e.includeBtc,
					includeStaking: e.includeStaking,
					includeCarry: e.includeCarry,
					includeDaily: e.includeDaily,
					includeTreasury: e.includeTreasury,
					includeOperating: e.includeOperating
				}) : W(t.status === "gap" && t.reason === "unavailable-or-unverified" && t.snapshot === void 0, "Invalid history gap");
			}), W(c.coverage.pageComplete === c.items.every((e) => e.status === "verified"), "Wrong history completeness"), c;
		},
		async operatingReceipt(e, t) {
			e = structuredClone(e), W(K(e.roundSlot) && typeof e.signature == "string" && /^[1-9A-HJ-NP-Za-km-z]{80,90}$/.test(e.signature) && e.operation === "collect", "Unsupported Operating receipt");
			let n = await a(t), i = await r(Gr + "/operating-receipt", e, t);
			if (o(i, n), W(n.identity.operating && i.schema === "wen-mining-operating-receipt/2" && i.trust === "pinned-finalized-rpc-not-state-proof" && [
				"roundSlot",
				"signature",
				"operation"
			].every((t) => i[t] === e[t]) && !Object.hasOwn(i, "paid") && i.costCoverage === "selected-transaction-only-payer-role-unclassified", "Wrong Operating receipt scope"), ["unknown", "pending"].includes(i.status)) return W(i.disposition === "unverified" && i.effects === null && i.payer === null && i.networkFeeLamports === null && i.feeVerified === !1 && i.originalTransactionVerified === !1 && i.currentAccountStateVerified === !1 && i.receiptProofVerified === !1 && i.slot === void 0 && i.currentAccountSlot === void 0, "Invalid unverified Operating receipt"), i;
			let s = (e) => typeof e == "string" && /^[0-9a-f]{64}$/.test(e), c = (e) => typeof e == "string" && /^(0|-?[1-9][0-9]*)$/.test(e) && BigInt(e) > -(1n << 64n) && BigInt(e) < 1n << 64n, l = i.effects;
			if (W(["finalized-success", "finalized-failed"].includes(i.status) && i.originalTransactionVerified === !0 && i.currentAccountStateVerified === !0 && i.feeVerified === !0 && K(i.networkFeeLamports) && s(i.payer) && s(i.messageSha256) && K(i.slot) && K(i.currentAccountSlot) && BigInt(i.slot) <= BigInt(i.currentAccountSlot), "Invalid Operating original proof"), W(l && l.recipient === n.identity.operating.recipient && [
				"amountLamports",
				"winnerFeesLamports",
				"forfeitureIncomeLamports",
				"pageRentChargedLamports",
				"pageRentRefundedLamports"
			].every((e) => K(l[e])) && c(l.payerNetDebitLamports) && BigInt(l.amountLamports) === BigInt(l.winnerFeesLamports) + BigInt(l.forfeitureIncomeLamports) && Array.isArray(l.solChanges) && new Set(l.solChanges.map((e) => e.address)).size === l.solChanges.length && l.solChanges.every((e) => s(e.address) && c(e.deltaLamports)), "Invalid Operating receipt effects"), W(BigInt(l.payerNetDebitLamports) === BigInt(i.networkFeeLamports) + BigInt(l.pageRentChargedLamports) - BigInt(l.pageRentRefundedLamports) - (i.payer === l.recipient ? BigInt(l.amountLamports) : 0n), "Operating payer debit mismatch"), i.status === "finalized-failed") W(i.disposition === "no-effect" && i.receiptProofVerified === !1 && [
				"amountLamports",
				"winnerFeesLamports",
				"forfeitureIncomeLamports",
				"pageRentChargedLamports",
				"pageRentRefundedLamports"
			].every((e) => l[e] === "0") && l.solChanges.length === 0, "Failed Operating transaction contains payments");
			else {
				W(i.disposition === "collected" && i.receiptProofVerified === !0 && BigInt(l.amountLamports) > 0n, "Missing Operating collection proof");
				let e = /* @__PURE__ */ new Map(), t = (t, n) => e.set(t, (e.get(t) ?? 0n) + n);
				t(n.identity.runtimeAddress, -BigInt(l.amountLamports)), t(l.recipient, BigInt(l.amountLamports)), t(i.payer, BigInt(l.pageRentRefundedLamports) - BigInt(l.pageRentChargedLamports));
				for (let [t, n] of e) W((l.solChanges.find((e) => e.address === t)?.deltaLamports ?? "0") === String(n), "Wrong Operating original SOL effect");
				W(l.solChanges.reduce((e, t) => e + BigInt(t.deltaLamports), 0n) === 0n, "Operating SOL conservation failed");
			}
			return i;
		},
		async treasuryReceipt(e, t) {
			e = structuredClone(e), W(K(e.roundSlot) && typeof e.signature == "string" && /^[1-9A-HJ-NP-Za-km-z]{80,90}$/.test(e.signature) && [
				"execute-buyback",
				"execute-liquidity",
				"expire-order",
				"convert-input"
			].includes(e.operation), "Unsupported treasury receipt");
			let n = e.operation === "convert-input" ? ["side", "nonce"] : [
				"day",
				"hour",
				"attempt"
			];
			W(e.operation === "convert-input" ? [0, 1].includes(e.side) && K(e.nonce) : K(e.day) && Number.isInteger(e.hour) && e.hour >= 1 && e.hour < 24 && Number.isInteger(e.attempt) && e.attempt >= 1 && e.attempt <= 3, "Invalid treasury selector");
			let i = await a(t), s = await r(Gr + "/treasury-receipt", e, t);
			if (o(s, i), W(i.identity.treasury && s.schema === "wen-mining-treasury-receipt/2" && s.trust === "pinned-finalized-rpc-not-state-proof" && [
				"roundSlot",
				"signature",
				"operation",
				...n
			].every((t) => s[t] === e[t]) && !Object.hasOwn(s, "paid"), "Wrong treasury receipt scope"), ["unknown", "pending"].includes(s.status)) return W(s.disposition === "unverified" && s.effects === null && s.payer === null && s.networkFeeLamports === null && s.feeVerified === !1 && s.originalTransactionVerified === !1 && s.currentAccountStateVerified === !1 && s.receiptProofVerified === !1 && s.slot === void 0 && s.currentAccountSlot === void 0, "Invalid unverified treasury receipt"), s;
			W(["finalized-success", "finalized-failed"].includes(s.status) && s.originalTransactionVerified === !0 && s.currentAccountStateVerified === !0 && s.feeVerified === !0 && K(s.networkFeeLamports) && /^[0-9a-f]{64}$/.test(s.payer) && /^[0-9a-f]{64}$/.test(s.messageSha256) && K(s.slot) && K(s.currentAccountSlot) && BigInt(s.slot) <= BigInt(s.currentAccountSlot), "Invalid treasury original proof");
			let c = s.effects, l = (e) => typeof e == "string" && /^(0|-?[1-9][0-9]*)$/.test(e) && BigInt(e) > -(1n << 64n) && BigInt(e) < 1n << 64n, u = (e) => typeof e == "string" && /^[0-9a-f]{64}$/.test(e);
			return W(c && Array.isArray(c.tokenChanges) && Array.isArray(c.solChanges) && K(c.lpMintedAtoms) && K(c.lpBurnedAtoms) && new Set(c.tokenChanges.map((e) => e.address)).size === c.tokenChanges.length && new Set(c.solChanges.map((e) => e.address)).size === c.solChanges.length && c.tokenChanges.every((e) => u(e.address) && u(e.mint) && l(e.deltaAtoms)) && c.solChanges.every((e) => u(e.address) && l(e.deltaLamports)), "Invalid treasury effects"), s.status === "finalized-failed" ? W(s.disposition === "no-effect" && s.receiptProofVerified === !1 && c.tokenChanges.length === 0 && c.solChanges.length === 0 && c.lpMintedAtoms === "0" && c.lpBurnedAtoms === "0" && c.reservationReleased === null, "Failed treasury transaction contains effects") : (W(s.receiptProofVerified === !0, "Missing treasury effect proof"), e.operation === "expire-order" ? W(s.disposition === "reservation-released" && c.tokenChanges.length === 0 && c.solChanges.length === 0 && c.lpMintedAtoms === "0" && c.lpBurnedAtoms === "0" && ["USDC", "SOL"].includes(c.reservationReleased?.asset) && K(c.reservationReleased.amountAtoms) && BigInt(c.reservationReleased.amountAtoms) > 0n, "Invalid reservation release") : (W(s.disposition === "executed" && c.reservationReleased === null && c.tokenChanges.length > 0, "Invalid treasury execution"), e.operation === "execute-liquidity" ? W(BigInt(c.lpMintedAtoms) > 0n && c.lpMintedAtoms === c.lpBurnedAtoms, "Missing LP mint and burn") : W(c.lpMintedAtoms === "0" && c.lpBurnedAtoms === "0", "Unexpected LP effects"))), s;
		},
		async receipt(e, t) {
			e = structuredClone(e), i(e), W([
				"ClaimSol",
				"ClaimSat",
				"ClaimBtc",
				"RecoverSol",
				"StakingClaimBtc",
				"StakingRecoverSol",
				"CarryClaimBtc",
				"CarryRefundBtc",
				"CarryRefundSol",
				"DailyPrizeBtc",
				"DailyPrizeSol",
				"DailyRefundBtc",
				"DailyRefundSol"
			].includes(e.operation) && Number.isSafeInteger(e.entry) && e.entry >= 0 && typeof e.signature == "string" && /^[1-9A-HJ-NP-Za-km-z]{80,90}$/.test(e.signature), "Unsupported receipt selection");
			let n = await a(t), s = await r(Gr + "/receipt", e, t);
			W(s?.schema === "wen-mining-wallet-receipt/2" && s.trust === "pinned-finalized-rpc-not-state-proof" && s.execution === "disabled" && s.source === n.source && G(s.identity, n.identity) && [
				"wallet",
				"roundSlot",
				"signature",
				"operation",
				"entry"
			].every((t) => s[t] === e[t]), "Wrong wallet receipt"), o(s, n);
			let c = [
				"principalLamports",
				"stakingRefundLamports",
				"satAtoms",
				"btcAtoms",
				"recoveredLamports"
			];
			if (W(s.amounts && c.every((e) => K(s.amounts[e])) && s.networkFeeLamports === s.amounts.networkFeeLamports, "Invalid receipt amounts"), [
				"finalized-success",
				"finalized-failed",
				"finalized-no-payment"
			].includes(s.status)) {
				if (W(/^[0-9a-f]{64}$/.test(s.messageSha256) && s.originalTransactionVerified === !0 && s.currentAccountStateVerified === !0 && s.feeVerified === !0 && K(s.amounts.networkFeeLamports) && K(s.slot) && K(s.currentAccountSlot) && BigInt(s.slot) <= BigInt(s.currentAccountSlot), "Invalid finalized disposition"), s.status === "finalized-success") {
					W(s.paid === !0 && s.receiptProofVerified === !0, "Payment proof missing");
					let t = {
						ClaimSol: ["principalLamports", "stakingRefundLamports"],
						ClaimSat: ["satAtoms"],
						ClaimBtc: ["btcAtoms"],
						RecoverSol: ["recoveredLamports"],
						StakingClaimBtc: ["btcAtoms"],
						StakingRecoverSol: ["recoveredLamports"],
						CarryClaimBtc: ["btcAtoms"],
						CarryRefundBtc: ["btcAtoms"],
						CarryRefundSol: ["recoveredLamports"],
						DailyPrizeBtc: ["btcAtoms"],
						DailyPrizeSol: ["recoveredLamports"],
						DailyRefundBtc: ["btcAtoms"],
						DailyRefundSol: ["recoveredLamports"]
					}[e.operation];
					W(t.reduce((e, t) => e + BigInt(s.amounts[t]), 0n) > 0n && c.filter((e) => !t.includes(e)).every((e) => s.amounts[e] === "0"), "Wrong operation payment amounts"), [
						"ClaimBtc",
						"StakingClaimBtc",
						"CarryClaimBtc",
						"CarryRefundBtc",
						"DailyPrizeBtc",
						"DailyRefundBtc"
					].includes(e.operation) && W(n.identity.payout && s.btcMint === n.identity.payout.btcMint && BigInt(s.amounts.btcAtoms) > 0n && K(s.cumulativeBtcPaidAtoms) && BigInt(s.cumulativeBtcPaidAtoms) >= BigInt(s.amounts.btcAtoms), "Invalid BTC payment"), [
						"RecoverSol",
						"StakingRecoverSol",
						"CarryRefundSol",
						"DailyPrizeSol",
						"DailyRefundSol"
					].includes(e.operation) && W(n.identity.payout && BigInt(s.amounts.recoveredLamports) > 0n, "Invalid SOL recovery");
				} else W(s.paid === !1 && s.receiptProofVerified === !1 && c.every((e) => s.amounts[e] === "0"), "Unpaid disposition contains payment"), W(s.status !== "finalized-no-payment" || e.operation === "RecoverSol", "Unexpected zero-payment operation");
			} else W(["unknown", "pending"].includes(s.status) && s.paid === !1 && s.receiptProofVerified === !1 && s.originalTransactionVerified === !1 && s.currentAccountStateVerified === !1 && s.feeVerified === !1 && s.amounts.networkFeeLamports === null && c.every((e) => s.amounts[e] === "0") && s.slot === void 0 && s.currentAccountSlot === void 0, "Invalid unverified status");
			return s;
		}
	};
}
//#endregion
//#region ../strategy-contract/src/strategy.ts
var Yr = Object.freeze({
	wide: Object.freeze([
		2e3,
		2e3,
		2e3,
		2e3,
		2e3
	]),
	center: Object.freeze([
		500,
		1500,
		6e3,
		1500,
		500
	]),
	"trend-up": Object.freeze([
		500,
		1e3,
		1500,
		4500,
		2500
	]),
	"trend-down": Object.freeze([
		2500,
		4500,
		1500,
		1e3,
		500
	]),
	tails: Object.freeze([
		4e3,
		1e3,
		0,
		1e3,
		4e3
	])
}), Xr = (1n << 64n) - 1n;
function q(e) {
	if (typeof e != "string" || !/^(0|[1-9][0-9]*)$/.test(e) || e.length > 20) throw Error("unsigned decimal required");
	let t = BigInt(e);
	if (t > Xr) throw Error("u64 overflow");
	return t;
}
function Zr(e) {
	if (typeof e != "string" || !/^(0|-?[1-9][0-9]*)$/.test(e) || e.length > 40) throw Error("signed decimal required");
	let t = BigInt(e);
	if (t < -(1n << 127n) || t >= 1n << 127n) throw Error("i128 overflow");
	return t;
}
function J(e) {
	if (typeof e != "string" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(e)) throw Error("UTC whole-second timestamp required");
	let t = Date.parse(e);
	if (!Number.isFinite(t) || new Date(t).toISOString() !== e.replace("Z", ".000Z")) throw Error("invalid calendar time");
	return t;
}
//#endregion
//#region ../strategy-contract/src/allocation.ts
function Qr(e) {
	if (!Array.isArray(e) || e.length !== 5 || e.some((e) => !Number.isInteger(e) || e < 0 || e > 1e4) || e.reduce((e, t) => e + t, 0) !== 1e4) throw Error("five weights must total 10000");
}
function $r(e, t) {
	let n = q(e);
	if (n === 0n) throw Error("positive amount required");
	Qr(t);
	let r = t.map((e) => n * BigInt(e) / 10000n), i = t.map((e, t) => ({
		i: t,
		remainder: n * BigInt(e) % 10000n
	})).sort((e, t) => e.remainder === t.remainder ? e.i - t.i : e.remainder > t.remainder ? -1 : 1), a = Number(n - r.reduce((e, t) => e + t, 0n));
	for (let e = 0; e < a; e++) r[i[e].i]++;
	if (r.some((e, n) => t[n] > 0 && e === 0n || e > 0n && e < 10000000n)) throw Error("nonzero allocation below entry minimum");
	return r.map(String);
}
var ei = {
	type: "object",
	properties: {
		schema: { const: "wen-strategy/1" },
		name: {
			type: "string",
			minLength: 1,
			maxLength: 80
		},
		description: {
			type: "string",
			maxLength: 2e3
		},
		market: { $ref: "#/$defs/market" },
		research: { $ref: "#/$defs/research" },
		allocation: { $ref: "#/$defs/allocation" },
		schedule: { $ref: "#/$defs/schedule" },
		limits: { $ref: "#/$defs/limits" },
		execution: { $ref: "#/$defs/execution" }
	},
	required: [
		"schema",
		"name",
		"description",
		"market",
		"research",
		"allocation",
		"schedule",
		"limits",
		"execution"
	],
	additionalProperties: !1,
	$schema: "https://json-schema.org/draft/2020-12/schema",
	$id: "urn:wen:strategy:1",
	$defs: {
		condition: {
			type: "object",
			properties: {
				metric: {
					type: "string",
					enum: ["returnBps", "realizedVolBps"]
				},
				lookbackMinutes: {
					type: "integer",
					enum: [
						30,
						60,
						240,
						720,
						1440
					]
				},
				operator: {
					type: "string",
					enum: ["gte", "lte"]
				},
				valueBps: {
					type: "string",
					pattern: "^(0|-?[1-9][0-9]*)$",
					maxLength: 40
				}
			},
			required: [
				"metric",
				"lookbackMinutes",
				"operator",
				"valueBps"
			],
			additionalProperties: !1
		},
		market: {
			type: "object",
			properties: {
				networkGenesisHash: {
					type: "string",
					minLength: 1,
					maxLength: 256
				},
				programId: {
					type: "string",
					minLength: 1,
					maxLength: 256
				},
				economyId: {
					type: "string",
					minLength: 1,
					maxLength: 256
				},
				assetMint: {
					type: "string",
					minLength: 1,
					maxLength: 256
				},
				termsHash: {
					type: "string",
					minLength: 1,
					maxLength: 256
				},
				interfaceVersion: {
					type: "string",
					minLength: 1,
					maxLength: 256
				}
			},
			required: [
				"networkGenesisHash",
				"programId",
				"economyId",
				"assetMint",
				"termsHash",
				"interfaceVersion"
			],
			additionalProperties: !1
		},
		allocation: {
			type: "object",
			properties: {
				preset: {
					type: "string",
					enum: [
						"wide",
						"center",
						"trend-up",
						"trend-down",
						"tails",
						"custom"
					]
				},
				weightsBps: {
					type: "array",
					items: {
						type: "integer",
						minimum: 0,
						maximum: 1e4
					},
					minItems: 5,
					maxItems: 5
				},
				mode: {
					type: "string",
					enum: ["fixed", "conditional"]
				},
				rules: {
					type: "array",
					items: {
						type: "object",
						properties: {
							when: {
								type: "array",
								items: { $ref: "#/$defs/condition" },
								maxItems: 32,
								minItems: 1
							},
							preset: {
								type: "string",
								enum: [
									"wide",
									"center",
									"trend-up",
									"trend-down",
									"tails",
									"custom"
								]
							},
							weightsBps: {
								type: "array",
								items: {
									type: "integer",
									minimum: 0,
									maximum: 1e4
								},
								minItems: 5,
								maxItems: 5
							}
						},
						required: [
							"when",
							"preset",
							"weightsBps"
						],
						additionalProperties: !1
					},
					maxItems: 8
				}
			},
			required: [
				"preset",
				"weightsBps",
				"mode",
				"rules"
			],
			additionalProperties: !1
		},
		limits: {
			type: "object",
			properties: {
				perRoundLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				totalTurnoverLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				dailyTurnoverLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				maxUnsettledLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				maxUserFeesLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				maxUserFeesPerRoundLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				maxLossLamports: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				}
			},
			required: [
				"perRoundLamports",
				"totalTurnoverLamports",
				"dailyTurnoverLamports",
				"maxUnsettledLamports",
				"maxUserFeesLamports",
				"maxUserFeesPerRoundLamports",
				"maxLossLamports"
			],
			additionalProperties: !1
		},
		schedule: {
			type: "object",
			properties: {
				startsAt: {
					type: "string",
					pattern: "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$",
					format: "date-time"
				},
				expiresAt: {
					type: "string",
					pattern: "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$",
					format: "date-time"
				},
				everyNthRound: {
					type: "integer",
					minimum: 1,
					maximum: 144
				},
				anchorRound: {
					type: "string",
					pattern: "^(0|[1-9][0-9]*)$",
					maxLength: 20
				},
				utcWindows: {
					type: "array",
					items: {
						type: "object",
						properties: {
							startMinute: {
								type: "integer",
								minimum: 0,
								maximum: 1439
							},
							endMinute: {
								type: "integer",
								minimum: 1,
								maximum: 1440
							}
						},
						required: ["startMinute", "endMinute"],
						additionalProperties: !1
					},
					maxItems: 1440
				},
				selectedRoundIds: {
					type: "array",
					items: {
						type: "string",
						pattern: "^(0|[1-9][0-9]*)$",
						maxLength: 20
					},
					maxItems: 144,
					uniqueItems: !0
				},
				skipRoundIds: {
					type: "array",
					items: {
						type: "string",
						pattern: "^(0|[1-9][0-9]*)$",
						maxLength: 20
					},
					maxItems: 144,
					uniqueItems: !0
				},
				maxRoundsTotal: {
					type: "integer",
					minimum: 1,
					maximum: 144
				},
				maxRoundsPerUtcDay: {
					type: "integer",
					minimum: 1,
					maximum: 144
				},
				cooldownRounds: {
					type: "integer",
					minimum: 0,
					maximum: 143
				},
				randomSelection: { anyOf: [{ type: "null" }, {
					type: "object",
					properties: {
						seedHex: {
							type: "string",
							pattern: "^[0-9a-f]{64}$"
						},
						count: {
							type: "integer",
							minimum: 1,
							maximum: 144
						}
					},
					required: ["seedHex", "count"],
					additionalProperties: !1
				}] }
			},
			required: [
				"startsAt",
				"expiresAt",
				"everyNthRound",
				"anchorRound",
				"utcWindows",
				"selectedRoundIds",
				"skipRoundIds",
				"maxRoundsTotal",
				"maxRoundsPerUtcDay",
				"cooldownRounds",
				"randomSelection"
			],
			additionalProperties: !1
		},
		research: {
			type: "object",
			properties: {
				thesis: {
					type: "string",
					enum: [
						"trend",
						"consolidation",
						"reversal",
						"volatility",
						"custom"
					]
				},
				horizonMinutes: {
					type: "integer",
					enum: [
						30,
						60,
						240,
						720,
						1440
					]
				},
				conditions: {
					type: "array",
					items: { $ref: "#/$defs/condition" },
					maxItems: 32
				},
				outlook: { anyOf: [{ type: "null" }, {
					type: "object",
					properties: {
						probabilitiesBps: {
							type: "array",
							items: {
								type: "integer",
								minimum: 0,
								maximum: 1e4
							},
							minItems: 5,
							maxItems: 5
						},
						source: {
							type: "string",
							enum: ["user", "model"]
						},
						methodVersion: {
							type: "string",
							minLength: 1,
							maxLength: 256
						},
						asOf: {
							type: "string",
							pattern: "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$",
							format: "date-time"
						}
					},
					required: [
						"probabilitiesBps",
						"source",
						"methodVersion",
						"asOf"
					],
					additionalProperties: !1
				}] }
			},
			required: [
				"thesis",
				"horizonMinutes",
				"conditions",
				"outlook"
			],
			additionalProperties: !1
		},
		execution: {
			type: "object",
			properties: {
				mode: {
					type: "string",
					enum: [
						"notify",
						"review",
						"bounded-auto"
					]
				},
				runner: {
					type: "string",
					enum: ["browser", "fased"]
				},
				mandateId: { anyOf: [{ type: "null" }, {
					type: "string",
					minLength: 1,
					maxLength: 256
				}] }
			},
			required: [
				"mode",
				"runner",
				"mandateId"
			],
			additionalProperties: !1
		}
	},
	$comment: "Additional exact-byte, u64/i128, sum, calendar, allocation minimum and relational validation is mandatory. A JSON Schema pass alone is not admission."
};
//#endregion
//#region ../strategy-contract/src/validation.ts
function ti(e) {
	if (e.length > 65536) throw Error("document exceeds 64 KiB");
	let t = new TextDecoder("utf-8", {
		fatal: !0,
		ignoreBOM: !0
	}).decode(e), n = 0, r = () => {
		for (; /[\x20\t\r\n]/.test(t[n] ?? "x");) n++;
	}, i = () => {
		let e = n++;
		for (; n < t.length;) {
			if (t[n] === "\\") {
				n += 2;
				continue;
			}
			if (t[n++] === "\"") {
				let r = JSON.parse(t.slice(e, n));
				if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(r)) throw Error("unpaired surrogate");
				return r;
			}
		}
		throw Error("unterminated string");
	}, a = (e = 0) => {
		if (e > 32) throw Error("JSON nesting");
		r();
		let o = t[n];
		if (o === "\"") return i();
		if (o === "{") {
			n++;
			let o = Object.create(null);
			if (r(), t[n] === "}") return n++, o;
			for (;;) {
				if (r(), t[n] !== "\"") throw Error("key required");
				let s = i();
				if (Object.hasOwn(o, s)) throw Error("duplicate key");
				if (r(), t[n++] !== ":") throw Error("colon required");
				o[s] = a(e + 1), r();
				let c = t[n++];
				if (c === "}") return o;
				if (c !== ",") throw Error("comma required");
			}
		}
		if (o === "[") {
			n++;
			let i = [];
			if (r(), t[n] === "]") return n++, i;
			for (;;) {
				i.push(a(e + 1)), r();
				let o = t[n++];
				if (o === "]") return i;
				if (o !== ",") throw Error("comma required");
			}
		}
		for (let [e, r] of [
			["true", !0],
			["false", !1],
			["null", null]
		]) if (t.slice(n, n + e.length) === e) return n += e.length, r;
		let s = /^-?(?:0|[1-9][0-9]*)/.exec(t.slice(n));
		if (!s) throw Error("JSON value");
		n += s[0].length;
		let c = Number(s[0]);
		if (!Number.isSafeInteger(c) || Object.is(c, -0)) throw Error("bounded integer required");
		return c;
	}, o = a();
	if (r(), n !== t.length) throw Error("trailing JSON");
	return o;
}
function ni(e, t) {
	if (t.$ref) {
		ni(e, ei.$defs[t.$ref.split("/").pop()]);
		return;
	}
	if (t.anyOf) {
		if (!t.anyOf.some((t) => {
			try {
				return ni(e, t), !0;
			} catch {
				return !1;
			}
		})) throw Error("union mismatch");
		return;
	}
	if ("const" in t && e !== t.const) throw Error("schema mismatch");
	if (t.enum && !t.enum.includes(e)) throw Error("unsupported enum");
	if (t.type === "null") {
		if (e !== null) throw Error("null expected");
		return;
	}
	if (t.type === "object") {
		if (e === null || Array.isArray(e) || typeof e != "object") throw Error("object expected");
		if (Object.keys(e).some((e) => !Object.hasOwn(t.properties, e)) || t.required.some((t) => !Object.hasOwn(e, t))) throw Error("unknown or missing field");
		for (let n of t.required) ni(e[n], t.properties[n]);
	} else if (t.type === "array") {
		if (!Array.isArray(e) || t.minItems !== void 0 && e.length < t.minItems || t.maxItems !== void 0 && e.length > t.maxItems) throw Error("array bounds");
		if (t.uniqueItems && new Set(e).size !== e.length) throw Error("duplicate array item");
		e.forEach((e) => ni(e, t.items));
	} else if (t.type === "string") {
		if (typeof e != "string" || t.minLength !== void 0 && [...e].length < t.minLength || t.maxLength !== void 0 && [...e].length > t.maxLength || t.pattern && !new RegExp(t.pattern).test(e)) throw Error("string bounds/format");
		t.format === "date-time" && J(e);
	} else if (t.type === "integer" && (!Number.isSafeInteger(e) || t.minimum !== void 0 && e < t.minimum || t.maximum !== void 0 && e > t.maximum)) throw Error("integer bounds");
}
function ri(e) {
	ni(e, ei.$defs.schedule);
	let t = J(e.expiresAt) - J(e.startsAt);
	if (t <= 0 || t > 864e5) throw Error("session duration");
	if (q(e.anchorRound), [...e.selectedRoundIds, ...e.skipRoundIds].forEach(q), e.selectedRoundIds.length + e.skipRoundIds.length > 144) throw Error("explicit slot limit");
	if (e.utcWindows.some((e) => e.startMinute >= e.endMinute)) throw Error("UTC window");
}
function ii(e) {
	ni(e, ei);
	let t = e, n = (e, n) => {
		if (Qr(n), e !== "custom" && n.some((t, n) => t !== Yr[e][n])) throw Error("modified preset must be custom");
		$r(t.limits.perRoundLamports, n);
	};
	for (let [e, n] of Object.entries(t.limits)) if (q(n) === 0n && !["maxUserFeesLamports", "maxUserFeesPerRoundLamports"].includes(e)) throw Error("positive budget");
	let r = t.limits;
	if (q(r.perRoundLamports) > q(r.dailyTurnoverLamports) || q(r.dailyTurnoverLamports) > q(r.totalTurnoverLamports) || q(r.perRoundLamports) > q(r.maxUnsettledLamports) || q(r.maxUserFeesPerRoundLamports) > q(r.maxUserFeesLamports)) throw Error("budget relationship");
	if (n(t.allocation.preset, t.allocation.weightsBps), t.allocation.mode === "fixed" && t.allocation.rules.length) throw Error("fixed allocation has rules");
	let i = [...t.research.conditions, ...t.allocation.rules.flatMap((e) => e.when)];
	if (i.length > 32) throw Error("condition limit");
	for (let e of i) {
		let t = Zr(e.valueBps);
		if (e.metric === "realizedVolBps" && t < 0n) throw Error("negative volatility");
	}
	for (let e of t.allocation.rules) n(e.preset, e.weightsBps);
	t.research.outlook && Qr(t.research.outlook.probabilitiesBps);
	let a = t.schedule, o = J(a.expiresAt) - J(a.startsAt);
	if (o <= 0 || o > 864e5) throw Error("session duration");
	if (q(a.anchorRound), [...a.selectedRoundIds, ...a.skipRoundIds].forEach(q), a.selectedRoundIds.length + a.skipRoundIds.length > 144) throw Error("explicit slot limit");
	if (a.utcWindows.some((e) => e.startMinute >= e.endMinute)) throw Error("UTC window");
	if (t.execution.mode === "bounded-auto" && t.execution.runner === "browser") throw Error("browser cannot run unattended");
	return t;
}
//#endregion
//#region ../strategy-contract/src/schedule.ts
function ai(e, t) {
	let n = q(t.roundId), r = J(t.opensAt), i = q(e.anchorRound);
	if (r < J(e.startsAt) || r >= J(e.expiresAt) || n < i || (n - i) % BigInt(e.everyNthRound) !== 0n || e.skipRoundIds.includes(t.roundId) || e.selectedRoundIds.length > 0 && !e.selectedRoundIds.includes(t.roundId)) return !1;
	let a = new Date(r), o = a.getUTCHours() * 60 + a.getUTCMinutes();
	return !e.utcWindows.length || e.utcWindows.some((e) => o >= e.startMinute && o < e.endMinute);
}
function oi(e, t) {
	if (ri(e), t.length > 144) throw Error("too many slots");
	let n = /* @__PURE__ */ new Set();
	return t.filter((t) => {
		if (n.has(t.roundId)) throw Error("duplicate slot");
		return n.add(t.roundId), ai(e, t);
	}).sort((e, t) => q(e.roundId) < q(t.roundId) ? -1 : 1);
}
//#endregion
//#region src/state.ts
var si = {
	amount: "",
	preset: "center",
	weights: [
		500,
		1500,
		6e3,
		1500,
		500
	],
	hours: 4,
	cadence: 1,
	maxRounds: "",
	turnover: "",
	unsettled: "",
	loss: "",
	fees: "",
	perFee: ""
};
function Y(e) {
	if (!/^(0|[1-9]\d*)(\.\d{1,9})?$/.test(e)) throw Error("Enter SOL with at most 9 decimal places.");
	let [t, n = ""] = e.split(".");
	return (BigInt(t) * 1000000000n + BigInt(n.padEnd(9, "0"))).toString();
}
function X(e) {
	let t = BigInt(e);
	return `${t / 1000000000n}.${(t % 1000000000n).toString().padStart(9, "0")}`.replace(/\.?0+$/, "") || "0";
}
function ci(e) {
	return $r(Y(e.amount), e.weights);
}
function li() {
	return {
		...si,
		amount: "5",
		preset: "wide",
		weights: [
			2e3,
			2e3,
			2e3,
			2e3,
			2e3
		],
		cadence: 3,
		maxRounds: "8",
		turnover: "40",
		unsettled: "10",
		loss: "4",
		fees: "0.01",
		perFee: "0.001"
	};
}
function ui(e) {
	let t = Number(e.maxRounds);
	if (!/^\d+$/.test(e.maxRounds)) throw Error("Set the maximum rounds.");
	let n = {
		schema: "wen-strategy/1",
		name: "Fixture session draft",
		description: "Fixture only. Execution disabled.",
		market: {
			networkGenesisHash: "fixture-network",
			programId: "fixture-program",
			economyId: "fixture-economy",
			assetMint: "fixture-mint",
			termsHash: "fixture-terms",
			interfaceVersion: "fixture-v1"
		},
		research: {
			thesis: "custom",
			horizonMinutes: 60,
			conditions: [],
			outlook: null
		},
		allocation: {
			preset: e.preset,
			weightsBps: e.weights,
			mode: "fixed",
			rules: []
		},
		schedule: {
			startsAt: "2026-10-22T00:00:00Z",
			expiresAt: `2026-10-22T${String(e.hours).padStart(2, "0")}:00:00Z`,
			everyNthRound: e.cadence,
			anchorRound: "0",
			utcWindows: [],
			selectedRoundIds: [],
			skipRoundIds: [],
			maxRoundsTotal: t,
			maxRoundsPerUtcDay: t,
			cooldownRounds: 0,
			randomSelection: null
		},
		limits: {
			perRoundLamports: Y(e.amount),
			totalTurnoverLamports: Y(e.turnover),
			dailyTurnoverLamports: Y(e.turnover),
			maxUnsettledLamports: Y(e.unsettled),
			maxLossLamports: Y(e.loss),
			maxUserFeesLamports: Y(e.fees),
			maxUserFeesPerRoundLamports: Y(e.perFee)
		},
		execution: {
			mode: "review",
			runner: "browser",
			mandateId: null
		}
	};
	return ii(n), n;
}
function di(e) {
	let t = ui(e), n = Array.from({ length: e.hours * 6 }, (e, t) => ({
		roundId: String(t),
		opensAt: new Date(Date.UTC(2026, 9, 22, 0, t * 10)).toISOString().replace(".000Z", "Z")
	})), r = oi(t.schedule, n).slice(0, t.schedule.maxRoundsTotal), i = BigInt(t.limits.perRoundLamports) * BigInt(r.length);
	if (i > BigInt(t.limits.totalTurnoverLamports)) throw Error("Selected rounds exceed the turnover cap.");
	return {
		strategy: t,
		slots: r,
		turnoverLamports: i.toString()
	};
}
function fi(e) {
	try {
		return Y(e.amount) === "5000000000" && e.weights.every((e) => e === 2e3);
	} catch {
		return !1;
	}
}
function pi(e) {
	if (!/^(0|[1-9]\d*)(\.\d{1,2})?$/.test(e)) throw Error("Use a percentage with at most 2 decimal places.");
	let [t, n = ""] = e.split("."), r = BigInt(t) * 100n + BigInt(n.padEnd(2, "0"));
	if (r > 10000n) throw Error("Percentage must be between 0 and 100.");
	return Number(r);
}
function mi() {
	let e = ui(li());
	return e.name = "Inspectable conditional fixture", e.allocation.mode = "conditional", e.allocation.rules = [{
		when: [{
			metric: "returnBps",
			lookbackMinutes: 60,
			operator: "gte",
			valueBps: "5"
		}],
		preset: "trend-up",
		weightsBps: [
			500,
			1e3,
			1500,
			4500,
			2500
		]
	}, {
		when: [{
			metric: "returnBps",
			lookbackMinutes: 60,
			operator: "lte",
			valueBps: "-5"
		}],
		preset: "trend-down",
		weightsBps: [
			2500,
			4500,
			1500,
			1e3,
			500
		]
	}], ii(e), e;
}
var hi = {
	source: "Canonical v2 supplied-state fixtures",
	fixtureSHA256: "0ea793559a0ea8aaeb77aa4955924868311aaa4f9b46a654a9fc05e93a649bc2",
	scenarios: [
		{
			name: "bootstrap",
			request: {
				binding: {
					economy: "0303030303030303030303030303030303030303030303030303030303030303",
					mint: "0404040404040404040404040404040404040404040404040404040404040404",
					network: "0101010101010101010101010101010101010101010101010101010101010101",
					program: "0202020202020202020202020202020202020202020202020202020202020202",
					terms: "0505050505050505050505050505050505050505050505050505050505050505"
				},
				eligibleStake: "0",
				entries: [
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
						selection: 0
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
						selection: 1
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
						selection: 2
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
						selection: 3
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
						selection: 4
					}
				],
				phase: "bootstrap",
				prices: {
					close: "10000000",
					open: "10000000"
				},
				priorBootstrapGross: "0",
				schema: "satcoin-mining-preview/2",
				slot: "0",
				state: {
					awarded: "0",
					boostAwarded: "0",
					boostPending: "0",
					boostRetired: !1,
					latestSlot: null,
					pending: "0"
				},
				year: {
					awarded: "0",
					pending: "0",
					schedule: null,
					year: "0"
				}
			},
			response: {
				budget: {
					carryInput: "0",
					dailyInput: "0",
					grossSat: "228310502280",
					lpSat: "114155251140",
					lpSol: "324000000",
					minerInput: "0",
					minerSat: "114155251140",
					operating: "36000000",
					stakingInput: "0",
					stakingRefund: "0",
					treasuryBtcInput: "0",
					treasuryBuybackInput: "0",
					treasuryLiquidityInput: "0"
				},
				evidence: "supplied-state-canonical-conditional",
				issuance: {
					annualAwarded: "228310502280",
					annualPending: "0",
					boostAwarded: "228310502280",
					lifetimeAwarded: "228310502280",
					lifetimePending: "0"
				},
				limits: {
					annual: "1500000000000000",
					boost: "60000000000000",
					lifetime: "30000000000000000"
				},
				outcome: 2,
				receipts: [
					{
						amount: "1000000000",
						creditPpm: "0",
						dailyScore: null,
						exact: !1,
						forfeiture: "100000000",
						lpSatContribution: "0",
						lpSolContribution: "90000000",
						minerInput: "0",
						nonce: "0",
						ordinal: "0",
						owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
						principal: "900000000",
						sat: "0",
						selection: 0,
						stakingRefund: "0",
						totalSolReturn: "900000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "500000",
						dailyScore: null,
						exact: !1,
						forfeiture: "75000000",
						lpSatContribution: "28538812785",
						lpSolContribution: "67500000",
						minerInput: "0",
						nonce: "0",
						ordinal: "1",
						owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
						principal: "925000000",
						sat: "28538812785",
						selection: 1,
						stakingRefund: "0",
						totalSolReturn: "925000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "1000000",
						dailyScore: null,
						exact: !0,
						forfeiture: "0",
						lpSatContribution: "57077625570",
						lpSolContribution: "9000000",
						minerInput: "0",
						nonce: "0",
						ordinal: "2",
						owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
						principal: "990000000",
						sat: "57077625570",
						selection: 2,
						stakingRefund: "0",
						totalSolReturn: "990000000",
						winnerFee: "10000000"
					},
					{
						amount: "1000000000",
						creditPpm: "500000",
						dailyScore: null,
						exact: !1,
						forfeiture: "75000000",
						lpSatContribution: "28538812785",
						lpSolContribution: "67500000",
						minerInput: "0",
						nonce: "0",
						ordinal: "3",
						owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
						principal: "925000000",
						sat: "28538812785",
						selection: 3,
						stakingRefund: "0",
						totalSolReturn: "925000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "0",
						dailyScore: null,
						exact: !1,
						forfeiture: "100000000",
						lpSatContribution: "0",
						lpSolContribution: "90000000",
						minerInput: "0",
						nonce: "0",
						ordinal: "4",
						owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
						principal: "900000000",
						sat: "0",
						selection: 4,
						stakingRefund: "0",
						totalSolReturn: "900000000",
						winnerFee: "0"
					}
				],
				request: {
					binding: {
						economy: "0303030303030303030303030303030303030303030303030303030303030303",
						mint: "0404040404040404040404040404040404040404040404040404040404040404",
						network: "0101010101010101010101010101010101010101010101010101010101010101",
						program: "0202020202020202020202020202020202020202020202020202020202020202",
						terms: "0505050505050505050505050505050505050505050505050505050505050505"
					},
					eligibleStake: "0",
					entries: [
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
							selection: 0
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
							selection: 1
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
							selection: 2
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
							selection: 3
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
							selection: 4
						}
					],
					phase: "bootstrap",
					prices: {
						close: "10000000",
						open: "10000000"
					},
					priorBootstrapGross: "0",
					schema: "satcoin-mining-preview/2",
					slot: "0",
					state: {
						awarded: "0",
						boostAwarded: "0",
						boostPending: "0",
						boostRetired: !1,
						latestSlot: null,
						pending: "0"
					},
					year: {
						awarded: "0",
						pending: "0",
						schedule: null,
						year: "0"
					}
				},
				reservedCeiling: "228310502280",
				schema: "satcoin-mining-preview/2"
			},
			inputSHA256: "26c8f43aacb37373c6c4e6f03c41ecc711464e2861d817fd056b9b76fa267e72"
		},
		{
			name: "normal-empty-stake",
			request: {
				binding: {
					economy: "0303030303030303030303030303030303030303030303030303030303030303",
					mint: "0404040404040404040404040404040404040404040404040404040404040404",
					network: "0101010101010101010101010101010101010101010101010101010101010101",
					program: "0202020202020202020202020202020202020202020202020202020202020202",
					terms: "0505050505050505050505050505050505050505050505050505050505050505"
				},
				eligibleStake: "0",
				entries: [
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
						selection: 0
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
						selection: 1
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
						selection: 2
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
						selection: 3
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
						selection: 4
					}
				],
				phase: "normal",
				prices: {
					close: "10000000",
					open: "10000000"
				},
				priorBootstrapGross: "30000000000000",
				schema: "satcoin-mining-preview/2",
				slot: "132",
				state: {
					awarded: "30000000000000",
					boostAwarded: "30000000000000",
					boostPending: "0",
					boostRetired: !0,
					latestSlot: null,
					pending: "0"
				},
				year: {
					awarded: "30000000000000",
					pending: "0",
					schedule: null,
					year: "0"
				}
			},
			response: {
				budget: {
					carryInput: "0",
					dailyInput: "3500000",
					grossSat: "28038452735",
					lpSat: "0",
					lpSol: "0",
					minerInput: "259000000",
					minerSat: "28038452735",
					operating: "45000000",
					stakingInput: "0",
					stakingRefund: "35000000",
					treasuryBtcInput: "8750000",
					treasuryBuybackInput: "4375000",
					treasuryLiquidityInput: "4375000"
				},
				evidence: "supplied-state-canonical-conditional",
				issuance: {
					annualAwarded: "30028038452735",
					annualPending: "0",
					boostAwarded: "30000000000000",
					lifetimeAwarded: "30028038452735",
					lifetimePending: "0"
				},
				limits: {
					annual: "1500000000000000",
					boost: "60000000000000",
					lifetime: "30000000000000000"
				},
				outcome: 2,
				receipts: [
					{
						amount: "1000000000",
						creditPpm: "0",
						dailyScore: "-90000000",
						exact: !1,
						forfeiture: "100000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "0",
						nonce: "0",
						ordinal: "0",
						owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
						principal: "900000000",
						sat: "0",
						selection: 0,
						stakingRefund: "10000000",
						totalSolReturn: "910000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "500000",
						dailyScore: "-2750000",
						exact: !1,
						forfeiture: "75000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "64750000",
						nonce: "0",
						ordinal: "1",
						owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
						principal: "925000000",
						sat: "7009613183",
						selection: 1,
						stakingRefund: "7500000",
						totalSolReturn: "932500000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "1000000",
						dailyScore: "119500000",
						exact: !0,
						forfeiture: "0",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "129500000",
						nonce: "0",
						ordinal: "2",
						owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
						principal: "990000000",
						sat: "14019226368",
						selection: 2,
						stakingRefund: "0",
						totalSolReturn: "990000000",
						winnerFee: "10000000"
					},
					{
						amount: "1000000000",
						creditPpm: "500000",
						dailyScore: "-2750000",
						exact: !1,
						forfeiture: "75000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "64750000",
						nonce: "0",
						ordinal: "3",
						owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
						principal: "925000000",
						sat: "7009613184",
						selection: 3,
						stakingRefund: "7500000",
						totalSolReturn: "932500000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "0",
						dailyScore: "-90000000",
						exact: !1,
						forfeiture: "100000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "0",
						nonce: "0",
						ordinal: "4",
						owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
						principal: "900000000",
						sat: "0",
						selection: 4,
						stakingRefund: "10000000",
						totalSolReturn: "910000000",
						winnerFee: "0"
					}
				],
				request: {
					binding: {
						economy: "0303030303030303030303030303030303030303030303030303030303030303",
						mint: "0404040404040404040404040404040404040404040404040404040404040404",
						network: "0101010101010101010101010101010101010101010101010101010101010101",
						program: "0202020202020202020202020202020202020202020202020202020202020202",
						terms: "0505050505050505050505050505050505050505050505050505050505050505"
					},
					eligibleStake: "0",
					entries: [
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
							selection: 0
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
							selection: 1
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
							selection: 2
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
							selection: 3
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
							selection: 4
						}
					],
					phase: "normal",
					prices: {
						close: "10000000",
						open: "10000000"
					},
					priorBootstrapGross: "30000000000000",
					schema: "satcoin-mining-preview/2",
					slot: "132",
					state: {
						awarded: "30000000000000",
						boostAwarded: "30000000000000",
						boostPending: "0",
						boostRetired: !0,
						latestSlot: null,
						pending: "0"
					},
					year: {
						awarded: "30000000000000",
						pending: "0",
						schedule: null,
						year: "0"
					}
				},
				reservedCeiling: "28038452735",
				schema: "satcoin-mining-preview/2"
			},
			inputSHA256: "b4dd44946f6b4faed608f505fb7b7a4e4ac3718e275555d7ff0a98c90b228f12"
		},
		{
			name: "normal-eligible",
			request: {
				binding: {
					economy: "0303030303030303030303030303030303030303030303030303030303030303",
					mint: "0404040404040404040404040404040404040404040404040404040404040404",
					network: "0101010101010101010101010101010101010101010101010101010101010101",
					program: "0202020202020202020202020202020202020202020202020202020202020202",
					terms: "0505050505050505050505050505050505050505050505050505050505050505"
				},
				eligibleStake: "1000000000",
				entries: [
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
						selection: 0
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
						selection: 1
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
						selection: 2
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
						selection: 3
					},
					{
						amount: "1000000000",
						nonce: "0",
						owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
						selection: 4
					}
				],
				phase: "normal",
				prices: {
					close: "10000000",
					open: "10000000"
				},
				priorBootstrapGross: "30000000000000",
				schema: "satcoin-mining-preview/2",
				slot: "132",
				state: {
					awarded: "30000000000000",
					boostAwarded: "30000000000000",
					boostPending: "0",
					boostRetired: !0,
					latestSlot: null,
					pending: "0"
				},
				year: {
					awarded: "30000000000000",
					pending: "0",
					schedule: null,
					year: "0"
				}
			},
			response: {
				budget: {
					carryInput: "0",
					dailyInput: "3500000",
					grossSat: "28038452735",
					lpSat: "0",
					lpSol: "0",
					minerInput: "259000000",
					minerSat: "28038452735",
					operating: "45000000",
					stakingInput: "35000000",
					stakingRefund: "0",
					treasuryBtcInput: "8750000",
					treasuryBuybackInput: "4375000",
					treasuryLiquidityInput: "4375000"
				},
				evidence: "supplied-state-canonical-conditional",
				issuance: {
					annualAwarded: "30028038452735",
					annualPending: "0",
					boostAwarded: "30000000000000",
					lifetimeAwarded: "30028038452735",
					lifetimePending: "0"
				},
				limits: {
					annual: "1500000000000000",
					boost: "60000000000000",
					lifetime: "30000000000000000"
				},
				outcome: 2,
				receipts: [
					{
						amount: "1000000000",
						creditPpm: "0",
						dailyScore: "-100000000",
						exact: !1,
						forfeiture: "100000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "0",
						nonce: "0",
						ordinal: "0",
						owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
						principal: "900000000",
						sat: "0",
						selection: 0,
						stakingRefund: "0",
						totalSolReturn: "900000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "500000",
						dailyScore: "-10250000",
						exact: !1,
						forfeiture: "75000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "64750000",
						nonce: "0",
						ordinal: "1",
						owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
						principal: "925000000",
						sat: "7009613183",
						selection: 1,
						stakingRefund: "0",
						totalSolReturn: "925000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "1000000",
						dailyScore: "119500000",
						exact: !0,
						forfeiture: "0",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "129500000",
						nonce: "0",
						ordinal: "2",
						owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
						principal: "990000000",
						sat: "14019226368",
						selection: 2,
						stakingRefund: "0",
						totalSolReturn: "990000000",
						winnerFee: "10000000"
					},
					{
						amount: "1000000000",
						creditPpm: "500000",
						dailyScore: "-10250000",
						exact: !1,
						forfeiture: "75000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "64750000",
						nonce: "0",
						ordinal: "3",
						owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
						principal: "925000000",
						sat: "7009613184",
						selection: 3,
						stakingRefund: "0",
						totalSolReturn: "925000000",
						winnerFee: "0"
					},
					{
						amount: "1000000000",
						creditPpm: "0",
						dailyScore: "-100000000",
						exact: !1,
						forfeiture: "100000000",
						lpSatContribution: "0",
						lpSolContribution: "0",
						minerInput: "0",
						nonce: "0",
						ordinal: "4",
						owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
						principal: "900000000",
						sat: "0",
						selection: 4,
						stakingRefund: "0",
						totalSolReturn: "900000000",
						winnerFee: "0"
					}
				],
				request: {
					binding: {
						economy: "0303030303030303030303030303030303030303030303030303030303030303",
						mint: "0404040404040404040404040404040404040404040404040404040404040404",
						network: "0101010101010101010101010101010101010101010101010101010101010101",
						program: "0202020202020202020202020202020202020202020202020202020202020202",
						terms: "0505050505050505050505050505050505050505050505050505050505050505"
					},
					eligibleStake: "1000000000",
					entries: [
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
							selection: 0
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
							selection: 1
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
							selection: 2
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
							selection: 3
						},
						{
							amount: "1000000000",
							nonce: "0",
							owner: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
							selection: 4
						}
					],
					phase: "normal",
					prices: {
						close: "10000000",
						open: "10000000"
					},
					priorBootstrapGross: "30000000000000",
					schema: "satcoin-mining-preview/2",
					slot: "132",
					state: {
						awarded: "30000000000000",
						boostAwarded: "30000000000000",
						boostPending: "0",
						boostRetired: !0,
						latestSlot: null,
						pending: "0"
					},
					year: {
						awarded: "30000000000000",
						pending: "0",
						schedule: null,
						year: "0"
					}
				},
				reservedCeiling: "28038452735",
				schema: "satcoin-mining-preview/2"
			},
			inputSHA256: "29b6ab2c846733fb069cbb582a87fbb6efad7827ff75bec76b96f157ea6d239f"
		}
	]
}, gi = {
	bootstrap: "Bootstrap",
	"normal-empty-stake": "Normal · no eligible stake",
	"normal-eligible": "Normal · eligible stake"
};
function _i({ value: e, onChange: r }) {
	return /* @__PURE__ */ n("label", {
		className: "wt-phase",
		children: ["Settlement fixture", /* @__PURE__ */ t("select", {
			"aria-label": "Settlement fixture",
			value: e,
			onChange: (e) => r(e.target.value),
			children: Object.entries(gi).map(([e, n]) => /* @__PURE__ */ t("option", {
				value: e,
				children: n
			}, e))
		})]
	});
}
function vi({ phase: e, matched: r, load: i, dynamic: a }) {
	let o = a?.envelope ?? hi.scenarios.find((t) => t.name === e);
	if (a && !a.envelope) return /* @__PURE__ */ n("div", {
		className: "wt-empty",
		"aria-live": "polite",
		children: [a.status === "loading" ? "Calculating canonical preview…" : a.error ?? "Enter a valid allocation to calculate.", /* @__PURE__ */ t("button", {
			onClick: a.refresh,
			disabled: a.status === "loading",
			children: "Calculate preview"
		})]
	});
	if (!a && !r || !o) return /* @__PURE__ */ n("div", {
		className: "wt-empty",
		children: ["No canonical payout fixture for this allocation.", i ? /* @__PURE__ */ t("button", {
			onClick: i,
			children: "Load 5 SOL Wide fixture"
		}) : null]
	});
	let { response: s } = o;
	return /* @__PURE__ */ n("section", {
		className: "wt-mining-preview",
		"aria-label": "Canonical mining preview",
		"data-phase": e,
		children: [
			/* @__PURE__ */ n("div", {
				className: "wt-heading",
				children: [/* @__PURE__ */ t("h3", { children: gi[e] }), /* @__PURE__ */ n("span", {
					className: "wt-tag",
					children: ["Local conditional fixture · outcome ", s.outcome === null ? "invalid" : "ABCDE"[s.outcome]]
				})]
			}),
			/* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				"aria-label": "Conditional returns",
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Range" }),
					/* @__PURE__ */ t("th", { children: "Committed SOL" }),
					/* @__PURE__ */ t("th", { children: "Principal SOL" }),
					/* @__PURE__ */ t("th", { children: "Staking refund SOL" }),
					/* @__PURE__ */ t("th", { children: "Total returned SOL" }),
					/* @__PURE__ */ t("th", { children: "Deduction / fee SOL" }),
					/* @__PURE__ */ t("th", { children: "Miner BTC funding SOL" }),
					/* @__PURE__ */ t("th", { children: "Miner SAT" }),
					/* @__PURE__ */ t("th", { children: "LP SAT" })
				] }) }), /* @__PURE__ */ t("tbody", { children: s.receipts.map((e) => /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", {
						scope: "row",
						children: "ABCDE"[e.selection ?? 0]
					}),
					/* @__PURE__ */ t("td", { children: X(e.amount) }),
					/* @__PURE__ */ t("td", { children: X(e.principal) }),
					/* @__PURE__ */ t("td", { children: X(e.stakingRefund) }),
					/* @__PURE__ */ t("td", { children: X(e.totalSolReturn) }),
					/* @__PURE__ */ n("td", { children: [
						X(e.forfeiture),
						" / ",
						X(e.winnerFee)
					] }),
					/* @__PURE__ */ t("td", { children: X(e.minerInput) }),
					/* @__PURE__ */ t("td", { children: X(e.sat) }),
					/* @__PURE__ */ t("td", { children: X(e.lpSatContribution) })
				] }, e.ordinal)) })] })
			}),
			/* @__PURE__ */ n("dl", {
				className: "wt-facts",
				children: [
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "BTC paid" }), /* @__PURE__ */ t("dd", { children: "Unavailable · conversion not executed" })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "LP SOL contribution" }), /* @__PURE__ */ n("dd", { children: [X(s.receipts.reduce((e, t) => (BigInt(e) + BigInt(t.lpSolContribution)).toString(), "0")), " SOL"] })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Execution" }), /* @__PURE__ */ t("dd", { children: "Disabled" })] })
				]
			}),
			a ? /* @__PURE__ */ n("div", {
				className: "wt-heading",
				children: [/* @__PURE__ */ t("span", {
					className: "wt-tag",
					children: "Review expires with context"
				}), /* @__PURE__ */ t("button", {
					onClick: a.refresh,
					children: "Recalculate"
				})]
			}) : null,
			/* @__PURE__ */ n("details", { children: [
				/* @__PURE__ */ t("summary", { children: "Source and assumptions" }),
				/* @__PURE__ */ n("p", { children: [a ? "Your selected entries only; no outside participants." : "Five separate 1 SOL entries, one in each range.", " Supplied state, BTC prices and crowding are fixtures. These are conditional entitlements, not paid rewards or a wallet balance."] }),
				/* @__PURE__ */ t("code", { children: o.inputSHA256 }),
				/* @__PURE__ */ t("p", { children: "Canonical v2 accounting and issuance schedule. Bootstrap and normal returns use their own terms." })
			] })
		]
	});
}
var yi = {
	source: "Local bank evidence",
	fixtureSHA256: "f09ce090d8add59bfd78c56822d3768a1aa16bd3c3ad1ff67383cde0f7610b2d",
	snapshots: [
		{
			id: "lifecycle",
			binding: {
				economy: "0707070707070707070707070707070707070707070707070707070707070707",
				ledger: "90f303c603bb1447e2163dcdfa23d6de644f94329c4bb581ccabf35578d13ff5",
				network: "0505050505050505050505050505050505050505050505050505050505050505",
				program: "d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6",
				terms: "0808080808080808080808080808080808080808080808080808080808080808"
			},
			assetMint: "7ec541dae147af27937f9a26e67fff69cedac284c78026847ee02a5b31b878f6",
			rounds: [
				{
					address: "0ec4f5860d8b4a9362eddd67c30f4dc7d24916a85a6dc8825d8372330782ebbb",
					slot: "15",
					phase: "normal",
					stage: 3,
					acceptedLamports: "6000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "5540000000",
					stakingRefundPaidLamports: "45000000",
					budget: {
						gross_sat: "28538812785",
						miner_sat: "28538812785",
						lp_sat: "0",
						lp_sol: "0",
						operating: "55000000",
						miner_input: "333000000",
						carry_input: "0",
						daily_input: "4500000",
						staking_input: "0",
						staking_refund: "45000000",
						treasury_btc_input: "11250000",
						treasury_buyback_input: "5625000",
						treasury_liquidity_input: "5625000"
					},
					reservation: {
						slot: "15",
						ceiling: "28538812785",
						boosted: !1,
						award: "28538812785"
					}
				},
				{
					address: "3896d788cb7aab46336685fa25beffb52ec1688458c7f39f25fb3847c1f34a22",
					slot: "159",
					phase: "normal",
					stage: 3,
					acceptedLamports: "9000000000",
					eligibleStakeAtoms: "1000000000",
					principalPaidLamports: "8380000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "28538812785",
						miner_sat: "28538812785",
						lp_sat: "0",
						lp_sol: "0",
						operating: "80000000",
						miner_input: "444000000",
						carry_input: "0",
						daily_input: "6000000",
						staking_input: "60000000",
						staking_refund: "0",
						treasury_btc_input: "15000000",
						treasury_buyback_input: "7500000",
						treasury_liquidity_input: "7500000"
					},
					reservation: {
						slot: "159",
						ceiling: "28538812785",
						boosted: !1,
						award: "28538812785"
					}
				},
				{
					address: "79c3e82e413d987d17ce6f406d77bbce11040067c6032c271bfc6d166c3f0779",
					slot: "3",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "4625000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "114155251140",
						miner_sat: "57077625570",
						lp_sat: "57077625570",
						lp_sol: "337500000",
						operating: "37500000",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "3",
						ceiling: "228310502280",
						boosted: !0,
						award: "114155251140"
					}
				},
				{
					address: "839b38f74597bf0d44650a65e60a0e43fee030c212ca002f8e8f56f145e3ade0",
					slot: "9",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "10000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "10000000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "0",
						miner_sat: "0",
						lp_sat: "0",
						lp_sol: "0",
						operating: "0",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "9",
						ceiling: "228310502280",
						boosted: !0,
						award: "0"
					}
				},
				{
					address: "b59e5dc7ee593bdbd469323d2e7dc3ff1e64d2d4d6ca41f1b56b974bcc91b3d2",
					slot: "6",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "4500000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "0",
						miner_sat: "0",
						lp_sat: "0",
						lp_sol: "450000000",
						operating: "50000000",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "6",
						ceiling: "228310502280",
						boosted: !0,
						award: "0"
					}
				},
				{
					address: "f1dc2dd11c8c852b46c19b1d467021f3ac46b614f1b77c5d6fa0a98fbf8367e3",
					slot: "0",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "4640000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "228310502280",
						miner_sat: "114155251140",
						lp_sat: "114155251140",
						lp_sol: "324000000",
						operating: "36000000",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "0",
						ceiling: "228310502280",
						boosted: !0,
						award: "228310502280"
					}
				},
				{
					address: "fc5ff157a965af334f2cc830854de31afcb27693d78c829b37c20625240bf516",
					slot: "12",
					phase: "normal",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "4640000000",
					stakingRefundPaidLamports: "35000000",
					budget: {
						gross_sat: "28538812785",
						miner_sat: "28538812785",
						lp_sat: "0",
						lp_sol: "0",
						operating: "45000000",
						miner_input: "259000000",
						carry_input: "0",
						daily_input: "3500000",
						staking_input: "0",
						staking_refund: "35000000",
						treasury_btc_input: "8750000",
						treasury_buyback_input: "4375000",
						treasury_liquidity_input: "4375000"
					},
					reservation: {
						slot: "12",
						ceiling: "28538812785",
						boosted: !1,
						award: "28538812785"
					}
				}
			],
			entries: [
				{
					address: "08c2b8bb52ee95e9006795ffa0508408238d7dcc2570ea64b2a645029d0b4c87",
					slot: "159",
					page: 1,
					ordinal: "8",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "normal",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "3567351599",
					satClaimedAtoms: "3567351599",
					minerConversionInputLamports: "55500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "bootstrap",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "bootstrap",
					range: 1,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "bootstrap",
					range: 2,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "bootstrap",
					range: 3,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "bootstrap",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "5",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "bootstrap",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "6",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "bootstrap",
					range: 1,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "7",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "bootstrap",
					range: 2,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "410b8e306a4ee7bf8c4e98583cfdcdcc9be1e935b4f327239afd1354b517f3e8",
					slot: "12",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "normal",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "410b8e306a4ee7bf8c4e98583cfdcdcc9be1e935b4f327239afd1354b517f3e8",
					slot: "12",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "normal",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "7500000",
					stakingRefundReturnedLamports: "7500000",
					satAwardedAtoms: "7134703196",
					satClaimedAtoms: "7134703196",
					minerConversionInputLamports: "64750000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "410b8e306a4ee7bf8c4e98583cfdcdcc9be1e935b4f327239afd1354b517f3e8",
					slot: "12",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "normal",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "14269406392",
					satClaimedAtoms: "14269406392",
					minerConversionInputLamports: "129500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "410b8e306a4ee7bf8c4e98583cfdcdcc9be1e935b4f327239afd1354b517f3e8",
					slot: "12",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "normal",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "7500000",
					stakingRefundReturnedLamports: "7500000",
					satAwardedAtoms: "7134703197",
					satClaimedAtoms: "7134703197",
					minerConversionInputLamports: "64750000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "410b8e306a4ee7bf8c4e98583cfdcdcc9be1e935b4f327239afd1354b517f3e8",
					slot: "12",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "normal",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "normal",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "normal",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "3567351598",
					satClaimedAtoms: "3567351598",
					minerConversionInputLamports: "55500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "normal",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "7134703196",
					satClaimedAtoms: "7134703196",
					minerConversionInputLamports: "111000000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "normal",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "3567351598",
					satClaimedAtoms: "3567351598",
					minerConversionInputLamports: "55500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "normal",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "5",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "normal",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "6",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "normal",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "3567351598",
					satClaimedAtoms: "3567351598",
					minerConversionInputLamports: "55500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "5159f9ab96b9ce8c3d4ff7a02c4037d8c32edc4200ecd08cf42178a800fec6dc",
					slot: "159",
					page: 0,
					ordinal: "7",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "normal",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "7134703196",
					satClaimedAtoms: "7134703196",
					minerConversionInputLamports: "111000000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "8251045457af128b8102e5fe2a47296c2508a6a9e414642e4b6d90fa8f3d0234",
					slot: "6",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "bootstrap",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "5000000000",
					deductionLamports: "500000000",
					feeLamports: "0",
					principalEntitlementLamports: "4500000000",
					principalReturnedLamports: "4500000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "450000000",
					lpSatContributionAtoms: "0"
				},
				{
					address: "9d7af52b1e68324e86d13c7d8acfa0b72e00de961ee479d37dd03a80cec2e554",
					slot: "0",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "bootstrap",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "90000000",
					lpSatContributionAtoms: "0"
				},
				{
					address: "9d7af52b1e68324e86d13c7d8acfa0b72e00de961ee479d37dd03a80cec2e554",
					slot: "0",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "bootstrap",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "28538812785",
					satClaimedAtoms: "28538812785",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "67500000",
					lpSatContributionAtoms: "28538812785"
				},
				{
					address: "9d7af52b1e68324e86d13c7d8acfa0b72e00de961ee479d37dd03a80cec2e554",
					slot: "0",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "bootstrap",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "57077625570",
					satClaimedAtoms: "57077625570",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "9000000",
					lpSatContributionAtoms: "57077625570"
				},
				{
					address: "9d7af52b1e68324e86d13c7d8acfa0b72e00de961ee479d37dd03a80cec2e554",
					slot: "0",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "bootstrap",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "28538812785",
					satClaimedAtoms: "28538812785",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "67500000",
					lpSatContributionAtoms: "28538812785"
				},
				{
					address: "9d7af52b1e68324e86d13c7d8acfa0b72e00de961ee479d37dd03a80cec2e554",
					slot: "0",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "bootstrap",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "90000000",
					lpSatContributionAtoms: "0"
				},
				{
					address: "b66c57d9c48f33e1eb556c50264f2f2b29b6a6df8dea64104992ea8787414999",
					slot: "15",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "normal",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "b66c57d9c48f33e1eb556c50264f2f2b29b6a6df8dea64104992ea8787414999",
					slot: "15",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "normal",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "7500000",
					stakingRefundReturnedLamports: "7500000",
					satAwardedAtoms: "7134703196",
					satClaimedAtoms: "7134703196",
					minerConversionInputLamports: "83250000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "b66c57d9c48f33e1eb556c50264f2f2b29b6a6df8dea64104992ea8787414999",
					slot: "15",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "normal",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "14269406392",
					satClaimedAtoms: "14269406392",
					minerConversionInputLamports: "166500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "b66c57d9c48f33e1eb556c50264f2f2b29b6a6df8dea64104992ea8787414999",
					slot: "15",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "normal",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "7500000",
					stakingRefundReturnedLamports: "7500000",
					satAwardedAtoms: "7134703197",
					satClaimedAtoms: "7134703197",
					minerConversionInputLamports: "83250000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "b66c57d9c48f33e1eb556c50264f2f2b29b6a6df8dea64104992ea8787414999",
					slot: "15",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "normal",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "b66c57d9c48f33e1eb556c50264f2f2b29b6a6df8dea64104992ea8787414999",
					slot: "15",
					page: 0,
					ordinal: "5",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "normal",
					range: null,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "ba73f5952a5f283f182ea89daf4f14a8343d5ef0390d08cb01932d4b70b459a0",
					slot: "3",
					page: 0,
					ordinal: "0",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "bootstrap",
					range: 2,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "5000000000",
					deductionLamports: "375000000",
					feeLamports: "0",
					principalEntitlementLamports: "4625000000",
					principalReturnedLamports: "4625000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "57077625570",
					satClaimedAtoms: "57077625570",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "337500000",
					lpSatContributionAtoms: "57077625570"
				},
				{
					address: "f62fac1534a845dc3dc745d0ee94b21e028665997b8fbc25a90be92a49ef08be",
					slot: "9",
					page: 1,
					ordinal: "8",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "bootstrap",
					range: 3,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "f62fac1534a845dc3dc745d0ee94b21e028665997b8fbc25a90be92a49ef08be",
					slot: "9",
					page: 1,
					ordinal: "9",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "bootstrap",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "1000000000",
					principalReturnedLamports: "1000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				}
			],
			payouts: [],
			treasuryStates: [],
			treasuryOrders: [],
			activations: [],
			inputSHA256: "672714b7b9a9e09175ac17390b705f26cf46389f92597761538494b9fa9ac14d"
		},
		{
			id: "graduation",
			binding: {
				economy: "0707070707070707070707070707070707070707070707070707070707070707",
				ledger: "90f303c603bb1447e2163dcdfa23d6de644f94329c4bb581ccabf35578d13ff5",
				network: "0505050505050505050505050505050505050505050505050505050505050505",
				program: "d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6",
				terms: "0808080808080808080808080808080808080808080808080808080808080808"
			},
			assetMint: "7ec541dae147af27937f9a26e67fff69cedac284c78026847ee02a5b31b878f6",
			rounds: [
				{
					address: "79c3e82e413d987d17ce6f406d77bbce11040067c6032c271bfc6d166c3f0779",
					slot: "3",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "4640000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "228310502280",
						miner_sat: "114155251140",
						lp_sat: "114155251140",
						lp_sol: "324000000",
						operating: "36000000",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "3",
						ceiling: "228310502280",
						boosted: !0,
						award: "228310502280"
					}
				},
				{
					address: "839b38f74597bf0d44650a65e60a0e43fee030c212ca002f8e8f56f145e3ade0",
					slot: "9",
					phase: "normal",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "4640000000",
					stakingRefundPaidLamports: "35000000",
					budget: {
						gross_sat: "27968481846",
						miner_sat: "27968481846",
						lp_sat: "0",
						lp_sol: "0",
						operating: "45000000",
						miner_input: "259000000",
						carry_input: "0",
						daily_input: "3500000",
						staking_input: "0",
						staking_refund: "35000000",
						treasury_btc_input: "8750000",
						treasury_buyback_input: "4375000",
						treasury_liquidity_input: "4375000"
					},
					reservation: {
						slot: "9",
						ceiling: "27968481846",
						boosted: !1,
						award: "27968481846"
					}
				},
				{
					address: "b59e5dc7ee593bdbd469323d2e7dc3ff1e64d2d4d6ca41f1b56b974bcc91b3d2",
					slot: "6",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "5000000000",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "5000000000",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "0",
						miner_sat: "0",
						lp_sat: "0",
						lp_sol: "0",
						operating: "0",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "6",
						ceiling: "228310502280",
						boosted: !0,
						award: "0"
					}
				},
				{
					address: "f1dc2dd11c8c852b46c19b1d467021f3ac46b614f1b77c5d6fa0a98fbf8367e3",
					slot: "0",
					phase: "bootstrap",
					stage: 3,
					acceptedLamports: "0",
					eligibleStakeAtoms: "0",
					principalPaidLamports: "0",
					stakingRefundPaidLamports: "0",
					budget: {
						gross_sat: "0",
						miner_sat: "0",
						lp_sat: "0",
						lp_sol: "0",
						operating: "0",
						miner_input: "0",
						carry_input: "0",
						daily_input: "0",
						staking_input: "0",
						staking_refund: "0",
						treasury_btc_input: "0",
						treasury_buyback_input: "0",
						treasury_liquidity_input: "0"
					},
					reservation: {
						slot: "0",
						ceiling: "228310502280",
						boosted: !0,
						award: "0"
					}
				}
			],
			entries: [
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "normal",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "normal",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "7500000",
					stakingRefundReturnedLamports: "7500000",
					satAwardedAtoms: "6992120461",
					satClaimedAtoms: "6992120461",
					minerConversionInputLamports: "64750000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "normal",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "13984240923",
					satClaimedAtoms: "13984240923",
					minerConversionInputLamports: "129500000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "normal",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "7500000",
					stakingRefundReturnedLamports: "7500000",
					satAwardedAtoms: "6992120462",
					satClaimedAtoms: "6992120462",
					minerConversionInputLamports: "64750000",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "1ddcbb35139050298e8038f29ca537ff986b823a4e253b81fac3ddb465c07446",
					slot: "9",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "normal",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "10000000",
					stakingRefundReturnedLamports: "10000000",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "8251045457af128b8102e5fe2a47296c2508a6a9e414642e4b6d90fa8f3d0234",
					slot: "6",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "bootstrap",
					range: null,
					creditPpm: "0",
					exact: !1,
					committedLamports: "5000000000",
					deductionLamports: "0",
					feeLamports: "0",
					principalEntitlementLamports: "5000000000",
					principalReturnedLamports: "5000000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "0",
					lpSatContributionAtoms: "0"
				},
				{
					address: "ba73f5952a5f283f182ea89daf4f14a8343d5ef0390d08cb01932d4b70b459a0",
					slot: "3",
					page: 0,
					ordinal: "0",
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					phase: "bootstrap",
					range: 0,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "90000000",
					lpSatContributionAtoms: "0"
				},
				{
					address: "ba73f5952a5f283f182ea89daf4f14a8343d5ef0390d08cb01932d4b70b459a0",
					slot: "3",
					page: 0,
					ordinal: "1",
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					phase: "bootstrap",
					range: 1,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "28538812785",
					satClaimedAtoms: "28538812785",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "67500000",
					lpSatContributionAtoms: "28538812785"
				},
				{
					address: "ba73f5952a5f283f182ea89daf4f14a8343d5ef0390d08cb01932d4b70b459a0",
					slot: "3",
					page: 0,
					ordinal: "2",
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					phase: "bootstrap",
					range: 2,
					creditPpm: "1000000",
					exact: !0,
					committedLamports: "1000000000",
					deductionLamports: "0",
					feeLamports: "10000000",
					principalEntitlementLamports: "990000000",
					principalReturnedLamports: "990000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "57077625570",
					satClaimedAtoms: "57077625570",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "9000000",
					lpSatContributionAtoms: "57077625570"
				},
				{
					address: "ba73f5952a5f283f182ea89daf4f14a8343d5ef0390d08cb01932d4b70b459a0",
					slot: "3",
					page: 0,
					ordinal: "3",
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					phase: "bootstrap",
					range: 3,
					creditPpm: "500000",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "75000000",
					feeLamports: "0",
					principalEntitlementLamports: "925000000",
					principalReturnedLamports: "925000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "28538812785",
					satClaimedAtoms: "28538812785",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "67500000",
					lpSatContributionAtoms: "28538812785"
				},
				{
					address: "ba73f5952a5f283f182ea89daf4f14a8343d5ef0390d08cb01932d4b70b459a0",
					slot: "3",
					page: 0,
					ordinal: "4",
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					phase: "bootstrap",
					range: 4,
					creditPpm: "0",
					exact: !1,
					committedLamports: "1000000000",
					deductionLamports: "100000000",
					feeLamports: "0",
					principalEntitlementLamports: "900000000",
					principalReturnedLamports: "900000000",
					stakingRefundLamports: "0",
					stakingRefundReturnedLamports: "0",
					satAwardedAtoms: "0",
					satClaimedAtoms: "0",
					minerConversionInputLamports: "0",
					lpSolContributionLamports: "90000000",
					lpSatContributionAtoms: "0"
				}
			],
			payouts: [],
			treasuryStates: [],
			treasuryOrders: [],
			activations: [{
				address: "d0254269a547a24367c891809175919f7a86ad277f89c4bfd16a5c9dd652acd2",
				identity: "95b3a56f91a81fd5ce89f9ac1988e02a9fa6e680cbdea914007bfdc4a733a022",
				pool: "0bd605d156a4836bd7255b6318666f9fd0bf99ef983885ce653059449a9b4118",
				lp_sol: "50323999999",
				lp_sat: "15114155251139",
				lp_burned: "872126566886",
				activated_at: "604920",
				normal_from: "9",
				setup_spent: "195611120"
			}],
			inputSHA256: "c74a6ad1258f21a55cb45f9efbd00a2772cf32463de0fb1f6e3ddc2937e4458d"
		},
		{
			id: "normal-payouts",
			binding: {
				economy: "0707070707070707070707070707070707070707070707070707070707070707",
				ledger: "90f303c603bb1447e2163dcdfa23d6de644f94329c4bb581ccabf35578d13ff5",
				network: "0505050505050505050505050505050505050505050505050505050505050505",
				program: "d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6",
				terms: "0808080808080808080808080808080808080808080808080808080808080808"
			},
			assetMint: "7ec541dae147af27937f9a26e67fff69cedac284c78026847ee02a5b31b878f6",
			rounds: [],
			entries: [],
			payouts: [
				{
					address: "53eb7511acce83e9a22d6441dbdb7b9800b86acde03ac779ab11d4a360729ffa",
					slot: "632",
					page: 0,
					index: 0,
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					inputLamports: "74000000",
					spentLamports: "74000000",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "7362",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "7362",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "53eb7511acce83e9a22d6441dbdb7b9800b86acde03ac779ab11d4a360729ffa",
					slot: "632",
					page: 0,
					index: 1,
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "6f1eee2b275f6bde5fe69ae216b840db134469e9414495e2d77b697fd2e56e8b",
					slot: "329",
					page: 0,
					index: 0,
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "6f1eee2b275f6bde5fe69ae216b840db134469e9414495e2d77b697fd2e56e8b",
					slot: "329",
					page: 0,
					index: 1,
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					inputLamports: "64750000",
					spentLamports: "0",
					pendingConversionLamports: "64750000",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "6f1eee2b275f6bde5fe69ae216b840db134469e9414495e2d77b697fd2e56e8b",
					slot: "329",
					page: 0,
					index: 2,
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					inputLamports: "129500000",
					spentLamports: "0",
					pendingConversionLamports: "129500000",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "6f1eee2b275f6bde5fe69ae216b840db134469e9414495e2d77b697fd2e56e8b",
					slot: "329",
					page: 0,
					index: 3,
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					inputLamports: "64750000",
					spentLamports: "0",
					pendingConversionLamports: "64750000",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "6f1eee2b275f6bde5fe69ae216b840db134469e9414495e2d77b697fd2e56e8b",
					slot: "329",
					page: 0,
					index: 4,
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 0,
					owner: "fa4834147f6e690c3693eff61336046403cd8ae2a14f31b3c407358569239565",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 1,
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					inputLamports: "59200000",
					spentLamports: "44400000",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "4427",
					btcPaidAtoms: "4427",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "14800000",
					recoveryClaimed: !0
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 2,
					owner: "4508a07aa941707f3eb2db94c8897a80b2c1197476b6de213ac273df7d86c4ff",
					inputLamports: "118400000",
					spentLamports: "88800000",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "8855",
					btcPaidAtoms: "8855",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "29600000",
					recoveryClaimed: !0
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 3,
					owner: "9475c6cf0eda34057528d8694781ecc92ad639d7e2bd27761ed3ef74924beb07",
					inputLamports: "118400000",
					spentLamports: "88800000",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "8855",
					btcPaidAtoms: "8855",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "29600000",
					recoveryClaimed: !0
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 4,
					owner: "a87b8a99bd88a69686c994a80b629d8154871aa295540834c01d79f4f916502f",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 5,
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 6,
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					inputLamports: "59200000",
					spentLamports: "44400000",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "4428",
					btcPaidAtoms: "4428",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "14800000",
					recoveryClaimed: !0
				},
				{
					address: "83c25a185acad59e2b08609d36fc00631b090ac522aff9a805bb59ff4193b3b0",
					slot: "29",
					page: 0,
					index: 7,
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				},
				{
					address: "8d98a2617d9d57cadf2e3fb0926efb2f47ffc9d1b10d4baaa57cc596a8fe398d",
					slot: "29",
					page: 1,
					index: 0,
					owner: "197f6b23e16c8532c6abc838facd5ea789be0c76b2920334039bfa8b3d368d61",
					inputLamports: "0",
					spentLamports: "0",
					pendingConversionLamports: "0",
					btcAcquiredAtoms: "0",
					btcPaidAtoms: "0",
					btcUnclaimedAtoms: "0",
					recoveredLamports: "0",
					recoveryClaimed: !1
				}
			],
			treasuryStates: [],
			treasuryOrders: [],
			activations: [],
			inputSHA256: "5828ed33269c311e54076b5e867e90b04eccbac657199e6c5d304a182db28869"
		},
		{
			id: "treasury",
			binding: {
				economy: "0707070707070707070707070707070707070707070707070707070707070707",
				ledger: "90f303c603bb1447e2163dcdfa23d6de644f94329c4bb581ccabf35578d13ff5",
				network: "0505050505050505050505050505050505050505050505050505050505050505",
				program: "d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6",
				terms: "0808080808080808080808080808080808080808080808080808080808080808"
			},
			assetMint: "7ec541dae147af27937f9a26e67fff69cedac284c78026847ee02a5b31b878f6",
			rounds: [],
			entries: [],
			payouts: [],
			treasuryStates: [{
				address: "271cb5f51219205728a32f1fe1b9ff8d87933fee5889789ef4386a3f10341708",
				btcMint: "0303030303030303030303030303030303030303030303030303030303030303",
				usdcMint: "0404040404040404040404040404040404040404040404040404040404040404",
				btcHeldAtoms: "8727",
				lockedSatAtoms: "28230792",
				liquiditySatAtoms: "4",
				liquiditySolLamports: "35447231",
				liquidityWrappedLamports: "4894",
				buybackUsdcAtoms: "4333479",
				sourceSolLamports: ["0", "0"],
				reserved: ["0", "0"],
				pending: null,
				paused: !1,
				day: "11"
			}],
			treasuryOrders: [
				{
					address: "0f1672df3a3d05584e8dcd868299d399b25a84fa07f854b223cc67fa5ec92539",
					day: "8",
					hour: 3,
					side: 0,
					phase: 2,
					attempts: 3,
					deadline: "704050",
					input: "29993",
					acquired: "0",
					sol_deposited: "0",
					sat_deposited: "0",
					lp_burned: "0"
				},
				{
					address: "2d36401ec6e800bb25b6a54710dbbf78107f3add4a4843fa1cee7e325c41d49f",
					day: "9",
					hour: 1,
					side: 1,
					phase: 1,
					attempts: 1,
					deadline: "781210",
					input: "3938037",
					acquired: "183341305",
					sol_deposited: "1964125",
					sat_deposited: "183341304",
					lp_burned: "18967609"
				},
				{
					address: "4040212efa97cb5b5590f2a2039df5062b6ff5cc8df11769d3efbd92941bde13",
					day: "8",
					hour: 1,
					side: 1,
					phase: 1,
					attempts: 1,
					deadline: "694810",
					input: "4375000",
					acquired: "655316059",
					sol_deposited: "2182126",
					sat_deposited: "655316056",
					lp_burned: "37815102"
				},
				{
					address: "6eae985da50e24fc8a48f6a52df8fd5834e2f349904b48cdeb5695baa5e4c3f7",
					day: "9",
					hour: 23,
					side: 0,
					phase: 2,
					attempts: 1,
					deadline: "863950",
					input: "29993",
					acquired: "0",
					sol_deposited: "0",
					sat_deposited: "0",
					lp_burned: "0"
				},
				{
					address: "c6711e089688e3654a725f89a398322896f73b88ca7194e46bf5d7b4526fcdba",
					day: "8",
					hour: 2,
					side: 0,
					phase: 1,
					attempts: 1,
					deadline: "700330",
					input: "30393",
					acquired: "28230792",
					sol_deposited: "0",
					sat_deposited: "0",
					lp_burned: "0"
				}
			],
			activations: [],
			inputSHA256: "18015e41fbd726e2cd01158205fc327389275291931e99080029b078e8c6c035"
		}
	]
}, bi = (e) => {
	let t = BigInt(e);
	return `${t / 100000000n}.${(t % 100000000n).toString().padStart(8, "0")}`;
}, xi = (e) => `${e.slice(0, 6)}…${e.slice(-6)}`;
function Si() {
	let [r, i] = o(yi.snapshots[0]?.id ?? ""), a = yi.snapshots.find((e) => e.id === r);
	return a ? /* @__PURE__ */ n("section", {
		className: "wt-mining-preview",
		"aria-label": "Canonical local records",
		children: [
			/* @__PURE__ */ n("label", {
				className: "wt-phase",
				children: ["Recorded case", /* @__PURE__ */ t("select", {
					"aria-label": "Recorded case",
					value: r,
					onChange: (e) => i(e.target.value),
					children: yi.snapshots.map((e) => /* @__PURE__ */ t("option", {
						value: e.id,
						children: e.id
					}, e.id))
				})]
			}),
			/* @__PURE__ */ t("span", {
				className: "wt-tag",
				children: "Local bank evidence · not wallet history"
			}),
			/* @__PURE__ */ n("details", { children: [/* @__PURE__ */ t("summary", { children: "Asset identity and source" }), /* @__PURE__ */ n("dl", {
				className: "wt-facts",
				children: [
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Mint" }), /* @__PURE__ */ t("dd", { children: /* @__PURE__ */ t("code", { children: a.assetMint }) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Program / economy / terms" }), /* @__PURE__ */ t("dd", { children: /* @__PURE__ */ t("code", { children: JSON.stringify(a.binding) }) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Source digest" }), /* @__PURE__ */ t("dd", { children: /* @__PURE__ */ t("code", { children: a.inputSHA256 }) })] })
				]
			})] }),
			a.rounds.length > 0 ? /* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				"aria-label": "Recorded rounds",
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Round" }),
					/* @__PURE__ */ t("th", { children: "Phase" }),
					/* @__PURE__ */ t("th", { children: "State" }),
					/* @__PURE__ */ t("th", { children: "Accepted SOL" })
				] }) }), /* @__PURE__ */ t("tbody", { children: a.rounds.map((e, r) => /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("td", { children: e.slot }),
					/* @__PURE__ */ t("td", { children: e.phase }),
					/* @__PURE__ */ t("td", { children: [
						"Open",
						"Calculating",
						"Apportioning",
						"Finalized"
					][e.stage] ?? "Unknown" }),
					/* @__PURE__ */ t("td", { children: X(e.acceptedLamports) })
				] }, r)) })] })
			}) : null,
			a.entries.length > 0 ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("h3", { children: "Principal and SAT claims" }), /* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				"aria-label": "Recorded owner claims",
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Round / owner" }),
					/* @__PURE__ */ t("th", { children: "Committed SOL" }),
					/* @__PURE__ */ t("th", { children: "Principal paid SOL" }),
					/* @__PURE__ */ t("th", { children: "Staking refund earned / paid SOL" }),
					/* @__PURE__ */ t("th", { children: "SAT earned / claimed" })
				] }) }), /* @__PURE__ */ t("tbody", { children: a.entries.map((e, r) => /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ n("th", {
						scope: "row",
						children: [
							e.slot,
							" / ",
							/* @__PURE__ */ t("span", {
								title: e.owner,
								children: xi(e.owner)
							})
						]
					}),
					/* @__PURE__ */ t("td", { children: X(e.committedLamports) }),
					/* @__PURE__ */ t("td", { children: X(e.principalReturnedLamports) }),
					/* @__PURE__ */ n("td", { children: [
						X(e.stakingRefundLamports),
						" / ",
						X(e.stakingRefundReturnedLamports)
					] }),
					/* @__PURE__ */ n("td", { children: [
						X(e.satAwardedAtoms),
						" / ",
						X(e.satClaimedAtoms)
					] })
				] }, r)) })] })
			})] }) : null,
			a.payouts.length > 0 ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("h3", { children: "BTC conversions and payouts" }), /* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				"aria-label": "Recorded BTC payouts",
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Round / owner" }),
					/* @__PURE__ */ t("th", { children: "Pending input SOL" }),
					/* @__PURE__ */ t("th", { children: "Recovered SOL" }),
					/* @__PURE__ */ t("th", { children: "BTC acquired" }),
					/* @__PURE__ */ t("th", { children: "BTC paid" }),
					/* @__PURE__ */ t("th", { children: "BTC unclaimed" })
				] }) }), /* @__PURE__ */ t("tbody", { children: a.payouts.map((e, r) => /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ n("th", {
						scope: "row",
						children: [
							e.slot,
							" / ",
							/* @__PURE__ */ t("span", {
								title: e.owner,
								children: xi(e.owner)
							})
						]
					}),
					/* @__PURE__ */ t("td", { children: X(e.pendingConversionLamports) }),
					/* @__PURE__ */ t("td", { children: X(e.recoveredLamports) }),
					/* @__PURE__ */ t("td", { children: bi(e.btcAcquiredAtoms) }),
					/* @__PURE__ */ t("td", { children: bi(e.btcPaidAtoms) }),
					/* @__PURE__ */ t("td", { children: bi(e.btcUnclaimedAtoms) })
				] }, r)) })] })
			})] }) : null,
			a.treasuryStates.map((e, r) => /* @__PURE__ */ n("section", { children: [/* @__PURE__ */ t("h3", { children: "Treasury custody" }), /* @__PURE__ */ n("dl", {
				className: "wt-facts",
				children: [
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Held BTC" }), /* @__PURE__ */ t("dd", { children: bi(e.btcHeldAtoms) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Locked SAT" }), /* @__PURE__ */ t("dd", { children: X(e.lockedSatAtoms) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "LP inventory SAT" }), /* @__PURE__ */ t("dd", { children: X(e.liquiditySatAtoms) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "LP inventory SOL / wrapped SOL" }), /* @__PURE__ */ n("dd", { children: [
						X(e.liquiditySolLamports),
						" / ",
						X(e.liquidityWrappedLamports)
					] })] })
				]
			})] }, r)),
			a.treasuryOrders.length > 0 ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("h3", { children: "Treasury orders" }), /* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				"aria-label": "Recorded treasury orders",
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Day / hour" }),
					/* @__PURE__ */ t("th", { children: "Route" }),
					/* @__PURE__ */ t("th", { children: "State" }),
					/* @__PURE__ */ t("th", { children: "Attempts" }),
					/* @__PURE__ */ t("th", { children: "Protocol LP receipts" })
				] }) }), /* @__PURE__ */ t("tbody", { children: a.treasuryOrders.map((e, r) => /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ n("td", { children: [
						e.day,
						" / ",
						e.hour
					] }),
					/* @__PURE__ */ t("td", { children: e.side === 0 ? "Locked SAT buyback" : "SAT/SOL liquidity" }),
					/* @__PURE__ */ t("td", { children: [
						"Reserved",
						"Executed",
						"Expired"
					][e.phase] ?? "Unknown" }),
					/* @__PURE__ */ t("td", { children: e.attempts }),
					/* @__PURE__ */ t("td", { children: BigInt(e.lp_burned) > 0n ? "Burned" : "None" })
				] }, r)) })] })
			})] }) : null,
			/* @__PURE__ */ t("p", { children: "Each case has its own source state. These records are not combined into a portfolio balance. No claim or signing action is enabled." })
		]
	}) : /* @__PURE__ */ t("div", {
		className: "wt-empty",
		children: "No verified local records"
	});
}
//#endregion
//#region src/OperatingRecords.tsx
function Ci({ operating: r, roundSlot: i, receipt: a, disabled: s, onClear: c, onInspect: l }) {
	let [u, d] = o(""), f = r.selection, p = r.custody, m = a?.effects;
	return /* @__PURE__ */ n("section", {
		"aria-label": "Verified Operating collection",
		children: [
			/* @__PURE__ */ t("h3", { children: "Operating" }),
			/* @__PURE__ */ t("span", {
				className: "wt-tag",
				children: f.collected ? "Round collection recorded" : f.eligible ? "Round collection not recorded" : "No collectible round allocation"
			}),
			/* @__PURE__ */ n("dl", {
				className: "wt-facts",
				children: [
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Round allocation SOL" }), /* @__PURE__ */ t("dd", { children: X(f.amountLamports) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Exact-entry fees SOL" }), /* @__PURE__ */ t("dd", { children: X(f.winnerFeesLamports) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Forfeiture income SOL" }), /* @__PURE__ */ t("dd", { children: X(f.forfeitureIncomeLamports) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Recorded total collected SOL" }), /* @__PURE__ */ t("dd", { children: X(p.totalCollectedLamports) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Recorded uncollected SOL" }), /* @__PURE__ */ t("dd", { children: X(p.uncollectedOperatingLamports) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Collection recipient" }), /* @__PURE__ */ t("dd", {
						style: { overflowWrap: "anywhere" },
						children: r.pins.recipient
					})] })
				]
			}),
			/* @__PURE__ */ t("p", { children: "Protocol income, separate from wallet rewards. Totals repeat across round views. Complete cost history and profit are unavailable." }),
			/* @__PURE__ */ n("label", { children: ["Signature", /* @__PURE__ */ t("input", {
				"aria-label": "Operating receipt signature",
				value: u,
				onChange: (e) => {
					d(e.target.value), c();
				}
			})] }),
			/* @__PURE__ */ t("button", {
				disabled: s || !u,
				onClick: () => void l({
					roundSlot: i,
					signature: u,
					operation: "collect"
				}),
				children: "Inspect Operating receipt"
			}),
			a ? /* @__PURE__ */ n("section", {
				"aria-label": "Operating transaction disposition",
				children: [/* @__PURE__ */ t("h4", { children: {
					unknown: "Transaction unavailable",
					pending: "Not finalized",
					"finalized-success": "Operating collection verified",
					"finalized-failed": "Finalized collection failed"
				}[a.status] }), a.originalTransactionVerified && m ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ n("dl", {
					className: "wt-facts",
					children: [
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Transaction / account slot" }), /* @__PURE__ */ n("dd", { children: [
							a.slot,
							" / ",
							a.currentAccountSlot
						] })] }),
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Gross collected SOL" }), /* @__PURE__ */ t("dd", { children: X(m.amountLamports) })] }),
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Submitter network fee SOL" }), /* @__PURE__ */ t("dd", { children: X(a.networkFeeLamports) })] }),
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Page rent funded SOL" }), /* @__PURE__ */ t("dd", { children: X(m.pageRentChargedLamports) })] }),
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Page rent returned SOL" }), /* @__PURE__ */ t("dd", { children: X(m.pageRentRefundedLamports) })] }),
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Submitter net debit SOL" }), /* @__PURE__ */ t("dd", { children: ((e) => {
							let t = BigInt(e);
							return `${t < 0n ? "-" : ""}${X(String(t < 0n ? -t : t))}`;
						})(m.payerNetDebitLamports) })] }),
						/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Fee payer" }), /* @__PURE__ */ t("dd", {
							style: { overflowWrap: "anywhere" },
							children: a.payer
						})] })
					]
				}), /* @__PURE__ */ t("p", { children: "Rent remains account capital. The payer address does not establish who bears protocol costs." })] }) : /* @__PURE__ */ t("p", { children: "Original execution and costs are unverified." })]
			}) : null
		]
	});
}
//#endregion
//#region src/TreasuryRecords.tsx
var Z = (e, t = 9) => {
	let n = BigInt(e), r = n < 0n ? -n : n, i = 10n ** BigInt(t);
	return `${n < 0n ? "-" : ""}${r / i}.${(r % i).toString().padStart(t, "0")}`;
}, wi = [
	"Conversion SOL",
	"BTC custody",
	"Buyback USDC",
	"Bridge SOL",
	"Locked SAT",
	"Liquidity SOL",
	"Liquidity SAT"
], Ti = [
	"Reserved",
	"Executed",
	"Reservation released"
];
function Ei({ treasury: a, assetMint: s, roundSlot: c, receipt: l, disabled: u, onInspect: d, onClear: f }) {
	let p = i(() => [...a.conversions.map((e) => ({
		key: `conversion:${e.value.side}:${e.value.nonce}`,
		label: `${e.value.side === 0 ? "BTC" : "USDC"} conversion ${e.value.nonce}`,
		selector: {
			operation: "convert-input",
			side: e.value.side,
			nonce: e.value.nonce
		}
	})), ...a.orders.flatMap((e) => e.attempts.flatMap((t) => [e.value.side === 0 ? "execute-buyback" : "execute-liquidity", ...t.value.number === e.value.attempts ? ["expire-order"] : []].map((n) => ({
		key: `${n}:${e.value.day}:${e.value.hour}:${t.value.number}`,
		label: `Day ${e.value.day} · ${e.value.hour}:00 · ${n === "expire-order" ? "Release reservation" : e.value.side === 0 ? "Buyback" : "Liquidity"} · attempt ${t.value.number}`,
		selector: {
			operation: n,
			day: e.value.day,
			hour: e.value.hour,
			attempt: t.value.number
		}
	}))))], [a]), [m, h] = o(""), [g, _] = o("");
	r(() => {
		h(p[0]?.key ?? ""), _("");
	}, [p]);
	let v = p.find((e) => e.key === m), y = (e) => e === a.state.value.btc_mint ? {
		label: "BTC",
		decimals: 8
	} : e === a.state.value.usdc_mint ? {
		label: "USDC",
		decimals: 6
	} : e === s ? {
		label: "SAT",
		decimals: 9
	} : e === a.vaults.find((e) => e.kind === 0)?.mint ? {
		label: "wSOL",
		decimals: 9
	} : {
		label: e.slice(0, 8) + "…",
		decimals: 9
	}, b = (e) => e === a.state.address ? "Treasury" : wi[a.vaults.find((t) => t.address === e)?.kind] ?? e.slice(0, 8) + "…";
	return /* @__PURE__ */ n("section", {
		"aria-label": "Verified treasury custody",
		children: [
			/* @__PURE__ */ t("h3", { children: "Protocol treasury" }),
			/* @__PURE__ */ t("p", {
				role: "status",
				children: a.status === "paused" ? "Treasury paused" : a.status === "order-pending" ? "Outstanding reservation" : "Treasury records verified"
			}),
			/* @__PURE__ */ t("p", { children: "Protocol-owned funds. These balances are not wallet rewards or withdrawal rights." }),
			/* @__PURE__ */ n("dl", {
				className: "wt-facts",
				children: [
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Unconverted BTC funding SOL" }), /* @__PURE__ */ t("dd", { children: Z(a.state.value.source_sol[0]) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Unconverted buyback funding SOL" }), /* @__PURE__ */ t("dd", { children: Z(a.state.value.source_sol[1]) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Unwrapped liquidity SOL" }), /* @__PURE__ */ t("dd", { children: Z(String(BigInt(a.state.value.liquidity_sol) - BigInt(a.state.value.liquidity_wrapped))) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Reserved buyback USDC" }), /* @__PURE__ */ t("dd", { children: Z(a.state.value.reserved[0], 6) })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Reserved liquidity SOL" }), /* @__PURE__ */ t("dd", { children: Z(a.state.value.reserved[1]) })] })
				]
			}),
			/* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Vault" }),
					/* @__PURE__ */ t("th", { children: "Accounted" }),
					/* @__PURE__ */ t("th", { children: "Excess" })
				] }) }), /* @__PURE__ */ t("tbody", { children: a.vaults.map((e) => /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("td", { children: wi[e.kind] }),
					/* @__PURE__ */ t("td", { children: Z(e.ownedAtoms, e.kind === 1 ? 8 : e.kind === 2 ? 6 : 9) }),
					/* @__PURE__ */ t("td", { children: Z(e.excessAtoms, e.kind === 1 ? 8 : e.kind === 2 ? 6 : 9) })
				] }, e.kind)) })] })
			}),
			/* @__PURE__ */ n("h4", { children: ["Orders · day ", a.dayId] }),
			/* @__PURE__ */ t("p", { children: "Includes any outstanding order from an earlier budget. Repeated round views do not add treasury balances." }),
			/* @__PURE__ */ t("div", {
				className: "wt-table-scroll",
				tabIndex: 0,
				children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
					/* @__PURE__ */ t("th", { children: "Day / hour" }),
					/* @__PURE__ */ t("th", { children: "Route" }),
					/* @__PURE__ */ t("th", { children: "State" }),
					/* @__PURE__ */ t("th", { children: "Input" }),
					/* @__PURE__ */ t("th", { children: "Locked SAT" }),
					/* @__PURE__ */ t("th", { children: "SOL / SAT added" }),
					/* @__PURE__ */ t("th", { children: "LP burned" })
				] }) }), /* @__PURE__ */ t("tbody", { children: a.orders.map((e) => {
					let r = e.value;
					return /* @__PURE__ */ n("tr", { children: [
						/* @__PURE__ */ n("td", { children: [
							r.day,
							" / ",
							r.hour
						] }),
						/* @__PURE__ */ t("td", { children: r.side === 0 ? "Buyback" : "Liquidity" }),
						/* @__PURE__ */ t("td", { children: Ti[r.phase] }),
						/* @__PURE__ */ n("td", { children: [
							Z(r.input, r.side === 0 ? 6 : 9),
							" ",
							r.side === 0 ? "USDC" : "SOL"
						] }),
						/* @__PURE__ */ t("td", { children: r.side === 0 ? Z(r.acquired) : "—" }),
						/* @__PURE__ */ n("td", { children: [
							Z(r.sol_deposited),
							" / ",
							Z(r.sat_deposited)
						] }),
						/* @__PURE__ */ t("td", { children: Z(r.lp_burned) })
					] }, e.address);
				}) })] })
			}),
			a.orders.length === 0 ? /* @__PURE__ */ t("p", { children: "No orders in the verified scope." }) : null,
			/* @__PURE__ */ t("h4", { children: "Inspect treasury execution" }),
			/* @__PURE__ */ n("div", {
				className: "wt-read-fields",
				children: [/* @__PURE__ */ n("label", { children: ["Record", /* @__PURE__ */ t("select", {
					"aria-label": "Treasury receipt record",
					value: m,
					onChange: (e) => {
						h(e.target.value), f();
					},
					children: p.map((e) => /* @__PURE__ */ t("option", {
						value: e.key,
						children: e.label
					}, e.key))
				})] }), /* @__PURE__ */ n("label", { children: ["Signature", /* @__PURE__ */ t("input", {
					"aria-label": "Treasury receipt signature",
					value: g,
					onChange: (e) => {
						_(e.target.value), f();
					}
				})] })]
			}),
			/* @__PURE__ */ t("button", {
				disabled: u || !v || !g,
				onClick: () => v && void d({
					roundSlot: c,
					signature: g,
					...v.selector
				}),
				children: "Inspect treasury receipt"
			}),
			l ? /* @__PURE__ */ n("section", {
				"aria-label": "Treasury transaction disposition",
				children: [
					/* @__PURE__ */ t("h4", { children: l.status === "finalized-success" ? l.disposition === "reservation-released" ? "Reservation release verified" : "Treasury execution verified" : l.status === "finalized-failed" ? "Treasury transaction failed" : l.status === "pending" ? "Treasury transaction not finalized" : "Treasury transaction unavailable" }),
					l.feeVerified ? /* @__PURE__ */ n("dl", {
						className: "wt-facts",
						children: [
							/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Transaction / account slot" }), /* @__PURE__ */ n("dd", { children: [
								l.slot,
								" / ",
								l.currentAccountSlot
							] })] }),
							/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Submitter network fee SOL" }), /* @__PURE__ */ t("dd", { children: Z(l.networkFeeLamports) })] }),
							/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Fee payer" }), /* @__PURE__ */ t("dd", {
								style: { overflowWrap: "anywhere" },
								children: l.payer
							})] })
						]
					}) : /* @__PURE__ */ t("p", { children: "Execution and transaction fee are unverified." }),
					l.receiptProofVerified && l.effects ? /* @__PURE__ */ t("section", {
						"aria-label": "Verified treasury effects",
						children: l.effects.reservationReleased ? /* @__PURE__ */ n("p", { children: [
							"Budget reservation released: ",
							Z(l.effects.reservationReleased.amountAtoms, l.effects.reservationReleased.asset === "USDC" ? 6 : 9),
							" ",
							l.effects.reservationReleased.asset,
							". No funds transferred."
						] }) : /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("div", {
							className: "wt-table-scroll",
							tabIndex: 0,
							children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("th", { children: "Account" }),
								/* @__PURE__ */ t("th", { children: "Asset" }),
								/* @__PURE__ */ t("th", { children: "Transaction change" })
							] }) }), /* @__PURE__ */ n("tbody", { children: [l.effects.tokenChanges.map((e) => {
								let r = y(e.mint);
								return /* @__PURE__ */ n("tr", { children: [
									/* @__PURE__ */ t("td", {
										title: e.address,
										children: b(e.address)
									}),
									/* @__PURE__ */ t("td", {
										title: e.mint,
										children: r.label
									}),
									/* @__PURE__ */ t("td", { children: Z(e.deltaAtoms, r.decimals) })
								] }, e.address);
							}), l.effects.solChanges.map((e) => /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("td", {
									title: e.address,
									children: b(e.address)
								}),
								/* @__PURE__ */ t("td", { children: "SOL" }),
								/* @__PURE__ */ t("td", { children: Z(e.deltaLamports) })
							] }, "native" + e.address))] })] })
						}), l.effects.lpBurnedAtoms === "0" ? null : /* @__PURE__ */ n("p", { children: [
							"LP minted ",
							Z(l.effects.lpMintedAtoms),
							" · burned ",
							Z(l.effects.lpBurnedAtoms)
						] })] })
					}) : null,
					/* @__PURE__ */ t("code", {
						style: { overflowWrap: "anywhere" },
						children: l.signature
					})
				]
			}) : null
		]
	});
}
//#endregion
//#region src/deployment-read-session.mjs
function Di(e, { now: t = Date.now, onChange: n = () => {} } = {}) {
	let r = 0, i = null, a = null, o = !1, s = {
		status: "idle",
		snapshot: null,
		receipt: null,
		error: null
	}, c = (e, t) => JSON.stringify({
		identity: e,
		wallet: t.wallet,
		roundSlot: t.roundSlot
	}), l = /* @__PURE__ */ new Map(), u = /* @__PURE__ */ new Map(), d = (e) => JSON.stringify(e), f = (e) => {
		s = e, o || n(e);
	}, p = () => {
		r++, i?.abort(), i = null;
	}, m = () => s.snapshot && t() >= s.snapshot.expiresAtMs, h = async (n, p = null) => {
		let h = p === "treasury", g = p === "operating", _ = h || g;
		if (!a || s.status !== "ready" || m() || !_ && n.wallet !== a.wallet || n.roundSlot !== a.roundSlot) throw Error("Refresh this wallet and round before inspecting a receipt.");
		if (h) {
			if (!a.includeTreasury || typeof e.treasuryReceipt != "function") throw Error("Refresh treasury scope before inspecting protocol execution.");
		} else if (g) {
			if (!a.includeOperating || typeof e.operatingReceipt != "function") throw Error("Refresh Operating scope before inspecting collection.");
		} else {
			if (["ClaimBtc", "RecoverSol"].includes(n.operation) && !a.includeBtc) throw Error("Refresh BTC conversion scope before inspecting a payout receipt.");
			if (["StakingClaimBtc", "StakingRecoverSol"].includes(n.operation) && !a.includeStaking) throw Error("Refresh staking scope before inspecting a beneficiary receipt.");
			if ([
				"CarryClaimBtc",
				"CarryRefundBtc",
				"CarryRefundSol"
			].includes(n.operation) && !a.includeCarry) throw Error("Refresh carry scope before inspecting a release or refund receipt.");
			if ([
				"DailyPrizeBtc",
				"DailyPrizeSol",
				"DailyRefundBtc",
				"DailyRefundSol"
			].includes(n.operation) && !a.includeDaily) throw Error("Refresh daily scope before inspecting a prize or contributor receipt.");
		}
		let v = d(a), y = r, b = s.snapshot;
		i = new AbortController(), f({
			...s,
			status: "checking",
			receipt: null,
			treasuryReceipt: null,
			operatingReceipt: null,
			error: null
		});
		try {
			let s = await (_ ? e[p + "Receipt"](n, i.signal) : e.receipt(n, i.signal));
			if (o || y !== r || d(a) !== v) return;
			if (t() >= b.expiresAtMs || t() >= s.expiresAtMs) throw Error("Receipt context expired.");
			let m = c(b.identity, a), x = u.get(m) ?? 0n;
			if (s.currentAccountSlot !== void 0) {
				let e = BigInt(s.currentAccountSlot);
				if (e < x) throw Error("Receipt account slot regressed.");
				u.set(m, e);
			}
			let S = d({
				scope: m,
				family: p,
				signature: n.signature,
				operation: n.operation,
				...h ? {
					day: n.day,
					hour: n.hour,
					attempt: n.attempt,
					side: n.side,
					nonce: n.nonce
				} : { entry: n.entry }
			}), C = l.get(S), w = d({
				status: s.status,
				message: s.messageSha256,
				..._ ? {
					effects: s.effects,
					disposition: s.disposition,
					payer: s.payer,
					fee: s.networkFeeLamports
				} : { amounts: s.amounts }
			});
			if (C && C !== w) throw Error("Finalized receipt changed or became unavailable. Refresh and reconcile.");
			[
				"finalized-success",
				"finalized-failed",
				"finalized-no-payment"
			].includes(s.status) && l.set(S, w), f({
				status: "ready",
				snapshot: b,
				receipt: _ ? null : s,
				treasuryReceipt: h ? s : null,
				operatingReceipt: g ? s : null,
				error: null
			});
		} catch (e) {
			!o && y === r && f({
				status: "error",
				snapshot: null,
				receipt: null,
				error: String(e?.message ?? e)
			});
		}
	};
	return {
		getState() {
			return m() ? (p(), f({
				status: "expired",
				snapshot: null,
				receipt: null,
				error: "Read expired. Refresh before inspecting a receipt."
			})) : (s.operatingReceipt ?? s.treasuryReceipt ?? s.receipt) && t() >= (s.operatingReceipt ?? s.treasuryReceipt ?? s.receipt).expiresAtMs && (p(), f({
				...s,
				status: "ready",
				receipt: null,
				treasuryReceipt: null,
				operatingReceipt: null,
				error: "Transaction status expired. Inspect again."
			})), s;
		},
		invalidate() {
			p(), a = null, f({
				status: "idle",
				snapshot: null,
				receipt: null,
				error: null
			});
		},
		clearReceipt() {
			p(), f({
				status: s.snapshot && !m() ? "ready" : "idle",
				snapshot: s.snapshot && !m() ? s.snapshot : null,
				receipt: null,
				treasuryReceipt: null,
				operatingReceipt: null,
				error: null
			});
		},
		async read(n) {
			p(), a = structuredClone(n);
			let s = d(a), l = r;
			i = new AbortController(), f({
				status: "loading",
				snapshot: null,
				receipt: null,
				error: null
			});
			try {
				let p = await e.read(n, i.signal);
				if (o || l !== r || d(a) !== s) return;
				if (t() >= p.expiresAtMs) throw Error("Read expired.");
				let m = c(p.identity, n), h = u.get(m) ?? 0n;
				if (BigInt(p.slot) < h) throw Error("Out-of-order finalized read.");
				u.set(m, BigInt(p.slot)), f({
					status: "ready",
					snapshot: p,
					receipt: null,
					error: null
				});
			} catch (e) {
				!o && l === r && f({
					status: "error",
					snapshot: null,
					receipt: null,
					error: String(e?.message ?? e)
				});
			}
		},
		inspect(e) {
			return h(e);
		},
		inspectTreasury(e) {
			return h(e, "treasury");
		},
		inspectOperating(e) {
			return h(e, "operating");
		},
		dispose() {
			p(), o = !0, a = null, s = {
				status: "idle",
				snapshot: null,
				receipt: null,
				error: null
			};
		}
	};
}
function Oi(e, { now: t = Date.now, onChange: n = () => {} } = {}) {
	let r = {
		status: "idle",
		page: null,
		error: null
	}, i = 0, a = null, o = !1, s = /* @__PURE__ */ new Map(), c = (e) => {
		r = e, o || n(e);
	}, l = () => {
		i++, a?.abort(), a = null;
	};
	return {
		getState() {
			return r.page && t() >= r.page.expiresAtMs && (l(), c({
				status: "expired",
				page: null,
				error: "History expired. Refresh the index."
			})), r;
		},
		invalidate() {
			l(), c({
				status: "idle",
				page: null,
				error: null
			});
		},
		async load(n) {
			l();
			let r = i;
			a = new AbortController(), c({
				status: "loading",
				page: null,
				error: null
			});
			try {
				let l = await e.history(structuredClone(n), a.signal);
				if (o || r !== i) return;
				if (t() >= l.expiresAtMs) throw Error("History expired.");
				let u = [];
				for (let e of l.items) {
					if (e.status !== "verified") continue;
					let r = e.snapshot, i = JSON.stringify({
						identity: l.identity,
						wallet: n.wallet,
						roundSlot: e.roundSlot
					}), a = BigInt(r.slot);
					if (t() >= r.expiresAtMs || a < (s.get(i) ?? 0n)) throw Error("History round expired or finalized slot regressed.");
					u.push([i, a]);
				}
				for (let [e, t] of u) s.set(e, t);
				c({
					status: "ready",
					page: l,
					error: null
				});
			} catch (e) {
				!o && r === i && c({
					status: "error",
					page: null,
					error: String(e?.message ?? e)
				});
			}
		},
		dispose() {
			l(), o = !0, r = {
				status: "idle",
				page: null,
				error: null
			};
		}
	};
}
//#endregion
//#region src/DeploymentRecords.tsx
function ki({ provider: i }) {
	let s = {
		unknown: "Transaction unavailable",
		pending: "Not finalized",
		"finalized-failed": "Finalized transaction failed",
		"finalized-no-payment": "Finalized · no payment"
	}, c = {
		"allocation-pending": "Allocation pending",
		recovering: "SOL recovery started",
		ready: "Conversion records verified",
		"awaiting-vaults": "Conversion not initialized"
	}, [l, u] = o(!1), [d, f] = o(!1), [p, m] = o(!1), [h, g] = o(!1), [_, v] = o(!1), [y, b] = o(!1), x = (e) => {
		let t = BigInt(e);
		return `${t / 100000000n}.${(t % 100000000n).toString().padStart(8, "0")}`;
	}, [S, C] = o(""), [w, T] = o(""), [E, D] = o(""), [O, k] = o("ClaimSol"), [A, ee] = o("0"), [j, M] = o({ status: "idle" }), [N, P] = o(null), [F, te] = o({ status: "idle" }), I = a(null);
	r(() => {
		if (te({
			status: "idle",
			page: null,
			error: null
		}), !i.history) return;
		let e = Oi(i, { onChange: te });
		I.current = e;
		let t = setInterval(() => e.getState(), 1e3);
		return () => {
			clearInterval(t), e.dispose(), I.current = null;
		};
	}, [i]);
	let ne = (e) => {
		P(null);
		try {
			I.current?.load({
				wallet: i.walletHex(S),
				includeBtc: l,
				includeStaking: d,
				includeCarry: p,
				includeDaily: h,
				includeTreasury: _,
				includeOperating: y,
				cursor: e
			});
		} catch (e) {
			P(e.message);
		}
	}, L = a(null);
	r(() => {
		let e = Di(i, { onChange: M });
		L.current = e, M(e.getState());
		let t = setInterval(() => e.getState(), 1e3);
		return () => {
			clearInterval(t), e.dispose(), L.current = null;
		};
	}, [i]);
	let re = () => ({
		wallet: i.walletHex(S),
		roundSlot: w,
		...l ? { includeBtc: !0 } : {},
		...d ? { includeStaking: !0 } : {},
		...p ? { includeCarry: !0 } : {},
		...h ? { includeDaily: !0 } : {},
		..._ ? { includeTreasury: !0 } : {},
		...y ? { includeOperating: !0 } : {}
	}), ie = (e, t) => {
		L.current?.invalidate(), I.current?.invalidate(), P(null), e(t);
	}, ae = () => {
		P(null);
		try {
			L.current?.read(re());
		} catch (e) {
			P(e.message);
		}
	}, oe = async () => {
		P(null);
		try {
			if (!/^(0|[1-9]\d*)$/.test(A)) throw Error("Enter a valid entry number.");
			await L.current?.inspect({
				wallet: i.walletHex(S),
				roundSlot: w,
				signature: E,
				operation: O,
				entry: Number(A)
			});
		} catch (e) {
			P(e.message);
		}
	}, R = j.snapshot, z = j.receipt;
	return /* @__PURE__ */ n("section", {
		className: "wt-mining-preview",
		"aria-label": "Deployment records",
		children: [
			/* @__PURE__ */ t("h3", { children: "Inspect wallet records" }),
			/* @__PURE__ */ t("span", {
				className: "wt-tag",
				children: "Read only · execution disabled"
			}),
			/* @__PURE__ */ n("div", {
				className: "wt-read-fields",
				children: [/* @__PURE__ */ n("label", { children: ["Public wallet address", /* @__PURE__ */ t("input", {
					"aria-label": "Read wallet address",
					value: S,
					onChange: (e) => ie(C, e.target.value)
				})] }), /* @__PURE__ */ n("label", { children: ["Round", /* @__PURE__ */ t("input", {
					"aria-label": "Read round",
					inputMode: "numeric",
					value: w,
					onChange: (e) => ie(T, e.target.value)
				})] })]
			}),
			/* @__PURE__ */ n("label", {
				className: "wt-read-toggle",
				children: [/* @__PURE__ */ t("input", {
					type: "checkbox",
					"aria-label": "Include BTC conversions",
					checked: l,
					onChange: (e) => {
						L.current?.invalidate(), I.current?.invalidate(), u(e.target.checked), k("ClaimSol"), P(null);
					}
				}), " Include BTC conversions"]
			}),
			/* @__PURE__ */ n("label", {
				className: "wt-read-toggle",
				children: [/* @__PURE__ */ t("input", {
					type: "checkbox",
					"aria-label": "Include staking rewards",
					checked: d,
					onChange: (e) => {
						L.current?.invalidate(), I.current?.invalidate(), f(e.target.checked), k("ClaimSol"), P(null);
					}
				}), " Include staking rewards"]
			}),
			/* @__PURE__ */ n("label", {
				className: "wt-read-toggle",
				children: [/* @__PURE__ */ t("input", {
					type: "checkbox",
					"aria-label": "Include carry rewards",
					checked: p,
					onChange: (e) => {
						L.current?.invalidate(), I.current?.invalidate(), m(e.target.checked), k("ClaimSol"), P(null);
					}
				}), " Include carry rewards"]
			}),
			/* @__PURE__ */ n("label", {
				className: "wt-read-toggle",
				children: [/* @__PURE__ */ t("input", {
					type: "checkbox",
					"aria-label": "Include daily prizes",
					checked: h,
					onChange: (e) => {
						L.current?.invalidate(), I.current?.invalidate(), g(e.target.checked), k("ClaimSol"), P(null);
					}
				}), " Include daily prizes"]
			}),
			/* @__PURE__ */ n("label", {
				className: "wt-read-toggle",
				children: [/* @__PURE__ */ t("input", {
					type: "checkbox",
					"aria-label": "Include treasury custody",
					checked: _,
					onChange: (e) => {
						L.current?.invalidate(), I.current?.invalidate(), v(e.target.checked), k("ClaimSol"), P(null);
					}
				}), " Include treasury custody"]
			}),
			/* @__PURE__ */ n("label", {
				className: "wt-read-toggle",
				children: [/* @__PURE__ */ t("input", {
					type: "checkbox",
					"aria-label": "Include Operating collection",
					checked: y,
					onChange: (e) => {
						L.current?.invalidate(), I.current?.invalidate(), b(e.target.checked), k("ClaimSol"), P(null);
					}
				}), " Include Operating collection"]
			}),
			/* @__PURE__ */ t("button", {
				onClick: ae,
				disabled: j.status === "loading" || j.status === "checking",
				children: "Refresh records"
			}),
			i.history ? /* @__PURE__ */ n("section", {
				"aria-label": "Indexed wallet history",
				children: [
					/* @__PURE__ */ t("h3", { children: "Indexed rounds" }),
					/* @__PURE__ */ t("button", {
						onClick: () => ne(null),
						disabled: F.status === "loading",
						children: "Refresh history"
					}),
					F.page?.nextCursor ? /* @__PURE__ */ t("button", {
						onClick: () => ne(F.page.nextCursor),
						children: "Next history page"
					}) : null,
					/* @__PURE__ */ t("div", {
						role: "status",
						children: F.status
					}),
					F.error ? /* @__PURE__ */ t("p", {
						role: "alert",
						children: F.error
					}) : null,
					F.page ? /* @__PURE__ */ n(e, { children: [
						/* @__PURE__ */ t("p", { children: "Partial index · separate finalized round snapshots · no combined balance" }),
						/* @__PURE__ */ t("span", {
							className: "wt-tag",
							children: F.page.source === "local-rpc-fixture" ? "Simulated RPC · indexed fixture" : "Pinned finalized RPC · partial index"
						}),
						/* @__PURE__ */ t("div", {
							className: "wt-table-scroll",
							tabIndex: 0,
							children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("th", { children: "Round" }),
								/* @__PURE__ */ t("th", { children: "Status" }),
								/* @__PURE__ */ t("th", { children: "Finalized slot" }),
								/* @__PURE__ */ t("th", { children: "Wallet entries" }),
								d ? /* @__PURE__ */ t("th", { children: "Staking beneficiaries" }) : null
							] }) }), /* @__PURE__ */ t("tbody", { children: F.page.items.map((e) => /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("td", { children: /* @__PURE__ */ t("button", {
									onClick: () => {
										L.current?.invalidate(), T(e.roundSlot), P(null);
									},
									children: e.roundSlot
								}) }),
								/* @__PURE__ */ t("td", { children: e.status === "verified" ? "Verified round" : "Unavailable or unverified" }),
								/* @__PURE__ */ t("td", { children: e.snapshot?.slot ?? "—" }),
								/* @__PURE__ */ t("td", { children: e.snapshot?.projection.entries.length ?? "—" }),
								d ? /* @__PURE__ */ t("td", { children: e.snapshot?.staking?.rows.length ?? "—" }) : null
							] }, e.roundSlot)) })] })
						}),
						F.page.items.length === 0 ? /* @__PURE__ */ t("p", { children: "No indexed rounds. This does not establish an empty wallet history." }) : null
					] }) : null
				]
			}) : null,
			/* @__PURE__ */ t("div", {
				role: "status",
				children: j.status
			}),
			j.error || N ? /* @__PURE__ */ t("p", {
				role: "alert",
				children: N ?? j.error
			}) : null,
			R ? /* @__PURE__ */ n(e, { children: [
				/* @__PURE__ */ t("span", {
					className: "wt-tag",
					children: R.source === "local-rpc-fixture" ? "Simulated RPC · local fixture" : "Pinned finalized RPC"
				}),
				/* @__PURE__ */ n("dl", {
					className: "wt-facts",
					children: [/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Account table finalized slot" }), /* @__PURE__ */ t("dd", { children: R.slot })] }), /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Coverage" }), /* @__PURE__ */ n("dd", { children: [
						"One settled round · ",
						R.coverage.entryCount,
						" entries checked"
					] })] })]
				}),
				/* @__PURE__ */ n("p", { children: [
					"Principal and SAT",
					l ? " · miner BTC" : "",
					d ? " · staking rewards" : "",
					p ? " · carry releases/refunds" : "",
					h ? " · daily prizes/refunds" : "",
					_ ? " · protocol treasury" : "",
					y ? " · Operating collection" : "",
					". Full wallet history is unavailable. RPC data is not an independent state proof."
				] }),
				/* @__PURE__ */ n("details", { children: [/* @__PURE__ */ t("summary", { children: "Deployment identity" }), /* @__PURE__ */ t("pre", {
					style: {
						whiteSpace: "pre-wrap",
						overflowWrap: "anywhere"
					},
					children: JSON.stringify(R.identity, null, 2)
				})] }),
				/* @__PURE__ */ n("div", {
					className: "wt-table-scroll",
					tabIndex: 0,
					"aria-label": "Verified round claims",
					children: [/* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
						/* @__PURE__ */ t("th", { children: "Entry" }),
						/* @__PURE__ */ t("th", { children: "Committed SOL" }),
						/* @__PURE__ */ t("th", { children: "Principal entitled / paid SOL" }),
						/* @__PURE__ */ t("th", { children: "Staking refund entitled / paid SOL" }),
						/* @__PURE__ */ t("th", { children: "SAT earned / claimed" })
					] }) }), /* @__PURE__ */ t("tbody", { children: R.projection.entries.map((e) => /* @__PURE__ */ n("tr", { children: [
						/* @__PURE__ */ t("td", { children: e.ordinal }),
						/* @__PURE__ */ t("td", { children: X(e.committedLamports) }),
						/* @__PURE__ */ n("td", { children: [
							X(e.principalEntitlementLamports),
							" / ",
							X(e.principalReturnedLamports)
						] }),
						/* @__PURE__ */ n("td", { children: [
							X(e.stakingRefundLamports),
							" / ",
							X(e.stakingRefundReturnedLamports)
						] }),
						/* @__PURE__ */ n("td", { children: [
							X(e.satAwardedAtoms),
							" / ",
							X(e.satClaimedAtoms)
						] })
					] }, e.ordinal)) })] }), R.projection.entries.length ? null : /* @__PURE__ */ t("p", { children: "No entries for this wallet in this round." })]
				}),
				R.btc ? /* @__PURE__ */ n("section", {
					"aria-label": "Verified BTC conversions",
					children: [
						/* @__PURE__ */ t("h3", { children: "BTC conversions and payouts" }),
						/* @__PURE__ */ t("div", {
							className: "wt-table-scroll",
							tabIndex: 0,
							children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("th", { children: "Entry" }),
								/* @__PURE__ */ t("th", { children: "Unsettled input SOL" }),
								/* @__PURE__ */ t("th", { children: "Recovered SOL" }),
								/* @__PURE__ */ t("th", { children: "BTC acquired" }),
								/* @__PURE__ */ t("th", { children: "BTC paid" }),
								/* @__PURE__ */ t("th", { children: "BTC unclaimed" })
							] }) }), /* @__PURE__ */ t("tbody", { children: R.projection.payouts.map((e) => /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("td", { children: e.ordinal }),
								/* @__PURE__ */ t("td", { children: X(e.pendingConversionLamports) }),
								/* @__PURE__ */ t("td", { children: X(e.recoveredLamports) }),
								/* @__PURE__ */ t("td", { children: x(e.btcAcquiredAtoms) }),
								/* @__PURE__ */ t("td", { children: x(e.btcPaidAtoms) }),
								/* @__PURE__ */ t("td", { children: x(e.btcUnclaimedAtoms) })
							] }, e.ordinal)) })] })
						}),
						/* @__PURE__ */ t("span", {
							className: "wt-tag",
							children: c[R.btc.status] ?? "Status unavailable"
						}),
						R.btc.state.value.pending ? /* @__PURE__ */ n("dl", {
							className: "wt-facts",
							children: [/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Round SOL converted, awaiting allocation" }), /* @__PURE__ */ t("dd", { children: X(R.btc.unallocatedSpentLamports) })] }), /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Round BTC awaiting allocation" }), /* @__PURE__ */ t("dd", { children: x(R.btc.unallocatedBtcAtoms) })] })]
						}) : null
					]
				}) : null,
				R.staking ? /* @__PURE__ */ n("section", {
					"aria-label": "Verified staking rewards",
					children: [/* @__PURE__ */ t("h3", { children: "Staking rewards" }), [
						"no-eligible-stakers",
						"bootstrap-no-staking",
						"no-staking-allocation"
					].includes(R.staking.status) ? /* @__PURE__ */ t("span", {
						className: "wt-tag",
						children: R.staking.status === "bootstrap-no-staking" ? "Bootstrap · no staking distribution" : R.staking.status === "no-staking-allocation" ? "No staking reward allocation" : "No eligible stake · contributor refunds apply"
					}) : /* @__PURE__ */ n(e, { children: [
						/* @__PURE__ */ n("span", {
							className: "wt-tag",
							children: ["Original round beneficiaries · ", c[R.staking.status]]
						}),
						/* @__PURE__ */ t("div", {
							className: "wt-table-scroll",
							tabIndex: 0,
							children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("th", { children: "Beneficiary" }),
								/* @__PURE__ */ t("th", { children: "Unsettled input SOL" }),
								/* @__PURE__ */ t("th", { children: "Recovered SOL" }),
								/* @__PURE__ */ t("th", { children: "BTC acquired" }),
								/* @__PURE__ */ t("th", { children: "BTC paid" }),
								/* @__PURE__ */ t("th", { children: "BTC unclaimed" })
							] }) }), /* @__PURE__ */ t("tbody", { children: R.staking.rows.map((e) => /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ t("td", { children: e.ordinal }),
								/* @__PURE__ */ t("td", { children: X(e.pendingConversionLamports) }),
								/* @__PURE__ */ t("td", { children: X(e.recoveredLamports) }),
								/* @__PURE__ */ t("td", { children: x(e.btcAcquiredAtoms) }),
								/* @__PURE__ */ t("td", { children: x(e.btcPaidAtoms) }),
								/* @__PURE__ */ t("td", { children: x(e.btcUnclaimedAtoms) })
							] }, e.ordinal)) })] })
						}),
						R.staking.rows.length === 0 ? /* @__PURE__ */ t("p", { children: "No staking beneficiary for this wallet in this round." }) : null,
						R.staking.status === "allocation-pending" ? /* @__PURE__ */ n("dl", {
							className: "wt-facts",
							children: [/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Cohort SOL converted, awaiting allocation" }), /* @__PURE__ */ t("dd", { children: X(R.staking.unallocatedSpentLamports) })] }), /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Cohort BTC awaiting allocation" }), /* @__PURE__ */ t("dd", { children: x(R.staking.unallocatedBtcAtoms) })] })]
						}) : null
					] })]
				}) : null,
				R.carry ? /* @__PURE__ */ n("section", {
					"aria-label": "Verified carry rewards",
					children: [
						/* @__PURE__ */ t("h3", { children: "Carry releases and refunds" }),
						/* @__PURE__ */ t("p", {
							role: "status",
							children: {
								"controller-unavailable": "Carry controller unavailable",
								"decision-unavailable": "Carry decision unavailable",
								collecting: "Carry held for future rounds",
								ready: "Carry release finalized",
								"release-allocation-pending": "Beneficiary allocation pending",
								refundable: "Contributor refunds allocated",
								"refund-allocation-pending": "Contributor allocation pending"
							}[R.carry.status]
						}),
						[["releaseRows", "Reserved beneficiary rewards"], ["refundRows", "Original contributor refunds"]].map(([e, r]) => /* @__PURE__ */ n("section", {
							"aria-label": r,
							children: [
								/* @__PURE__ */ t("h4", { children: r }),
								/* @__PURE__ */ t("div", {
									className: "wt-table-scroll",
									tabIndex: 0,
									children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
										/* @__PURE__ */ t("th", { children: "Entry" }),
										/* @__PURE__ */ t("th", { children: "SOL entitled / paid" }),
										/* @__PURE__ */ t("th", { children: "BTC entitled / paid" })
									] }) }), /* @__PURE__ */ t("tbody", { children: R.carry[e].map((e) => /* @__PURE__ */ n("tr", { children: [
										/* @__PURE__ */ t("td", { children: e.ordinal }),
										/* @__PURE__ */ n("td", { children: [
											X(e.solEntitlementLamports),
											" / ",
											X(e.solPaidLamports)
										] }),
										/* @__PURE__ */ n("td", { children: [
											x(e.btcEntitlementAtoms),
											" / ",
											x(e.btcPaidAtoms)
										] })
									] }, e.ordinal)) })] })
								}),
								R.carry[e].length === 0 ? /* @__PURE__ */ n("p", { children: [
									"No allocated ",
									e === "releaseRows" ? "release" : "refund",
									" record for this wallet."
								] }) : null
							]
						}, e))
					]
				}) : null,
				R.daily ? /* @__PURE__ */ n("section", {
					"aria-label": "Verified daily prizes",
					children: [
						/* @__PURE__ */ n("h3", { children: ["Daily prizes · day ", R.daily.dayId] }),
						/* @__PURE__ */ t("p", {
							role: "status",
							children: {
								"controller-unavailable": "Daily controller unavailable",
								"day-unavailable": "Day record unavailable",
								"scoring-pending": "Scoring pending",
								reserving: "Prize funding reserved",
								review: "Public review",
								correcting: "Score correction in progress",
								collecting: "Collecting prize funding",
								allocating: "Prize allocation pending",
								finalized: "Daily result finalized",
								"refund-allocation-pending": "Contributor allocation pending",
								refundable: "Contributor refunds allocated"
							}[R.daily.status]
						}),
						/* @__PURE__ */ t("p", { children: "Day awards repeat across this day’s round records; they are the same entitlement." }),
						[["prizeRows", "Daily award beneficiaries"], ["refundRows", "Daily contributor refunds"]].map(([e, r]) => /* @__PURE__ */ n("section", {
							"aria-label": r,
							children: [
								/* @__PURE__ */ t("h4", { children: r }),
								/* @__PURE__ */ t("div", {
									className: "wt-table-scroll",
									tabIndex: 0,
									children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
										/* @__PURE__ */ t("th", { children: e === "prizeRows" ? "Contest / member" : "Entry" }),
										/* @__PURE__ */ t("th", { children: "SOL entitled / paid" }),
										/* @__PURE__ */ t("th", { children: "BTC entitled / paid" })
									] }) }), /* @__PURE__ */ t("tbody", { children: R.daily[e].map((r) => /* @__PURE__ */ n("tr", { children: [
										/* @__PURE__ */ t("td", { children: e === "prizeRows" ? `${r.contestId} / ${r.memberOrdinal}` : r.ordinal }),
										/* @__PURE__ */ n("td", { children: [
											X(r.solEntitlementLamports),
											" / ",
											X(r.solPaidLamports)
										] }),
										/* @__PURE__ */ n("td", { children: [
											x(r.btcEntitlementAtoms),
											" / ",
											x(r.btcPaidAtoms)
										] })
									] }, r.ordinal)) })] })
								}),
								R.daily[e].length === 0 ? /* @__PURE__ */ n("p", { children: [
									"No allocated ",
									e === "prizeRows" ? "award" : "refund",
									" record for this wallet."
								] }) : null
							]
						}, e))
					]
				}) : null,
				R.treasury ? /* @__PURE__ */ t(Ei, {
					treasury: R.treasury,
					assetMint: R.identity.assetMint,
					roundSlot: w,
					receipt: j.treasuryReceipt,
					disabled: j.status !== "ready" || !i.treasuryReceipt,
					onClear: () => L.current?.clearReceipt(),
					onInspect: async (e) => {
						P(null);
						try {
							await L.current?.inspectTreasury(e);
						} catch (e) {
							P(e.message);
						}
					}
				}) : null,
				R.operating ? /* @__PURE__ */ t(Ci, {
					operating: R.operating,
					roundSlot: w,
					receipt: j.operatingReceipt,
					disabled: j.status !== "ready" || !i.operatingReceipt,
					onClear: () => L.current?.clearReceipt(),
					onInspect: async (e) => {
						P(null);
						try {
							await L.current?.inspectOperating(e);
						} catch (e) {
							P(e.message);
						}
					}
				}) : null,
				/* @__PURE__ */ t("h3", { children: "Inspect transaction receipt" }),
				/* @__PURE__ */ n("div", {
					className: "wt-read-fields",
					children: [
						/* @__PURE__ */ n("label", { children: ["Signature", /* @__PURE__ */ t("input", {
							"aria-label": "Receipt signature",
							value: E,
							onChange: (e) => {
								D(e.target.value), L.current?.clearReceipt();
							}
						})] }),
						/* @__PURE__ */ n("label", { children: ["Operation", /* @__PURE__ */ n("select", {
							"aria-label": "Receipt operation",
							value: O,
							onChange: (e) => {
								k(e.target.value), L.current?.clearReceipt();
							},
							children: [
								/* @__PURE__ */ t("option", {
									value: "ClaimSol",
									children: "Principal + staking refund"
								}),
								/* @__PURE__ */ t("option", {
									value: "ClaimSat",
									children: "SAT claim"
								}),
								R.btc ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("option", {
									value: "ClaimBtc",
									children: "BTC claim"
								}), /* @__PURE__ */ t("option", {
									value: "RecoverSol",
									children: "Unspent SOL recovery"
								})] }) : null,
								R.staking?.cohort ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("option", {
									value: "StakingClaimBtc",
									children: "Staking BTC claim"
								}), /* @__PURE__ */ t("option", {
									value: "StakingRecoverSol",
									children: "Staking SOL recovery"
								})] }) : null,
								R.carry?.releaseRows.length ? /* @__PURE__ */ t("option", {
									value: "CarryClaimBtc",
									children: "Carry BTC release"
								}) : null,
								R.carry?.refundRows.length ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("option", {
									value: "CarryRefundBtc",
									children: "Carry BTC refund"
								}), /* @__PURE__ */ t("option", {
									value: "CarryRefundSol",
									children: "Carry SOL refund"
								})] }) : null,
								R.daily?.prizeRows.length ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("option", {
									value: "DailyPrizeBtc",
									children: "Daily prize BTC"
								}), /* @__PURE__ */ t("option", {
									value: "DailyPrizeSol",
									children: "Daily prize SOL"
								})] }) : null,
								R.daily?.refundRows.length ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("option", {
									value: "DailyRefundBtc",
									children: "Daily BTC refund"
								}), /* @__PURE__ */ t("option", {
									value: "DailyRefundSol",
									children: "Daily SOL refund"
								})] }) : null
							]
						})] }),
						/* @__PURE__ */ n("label", { children: [O.startsWith("Staking") ? "Beneficiary ordinal" : O.startsWith("DailyPrize") ? "Member ordinal" : "Entry", /* @__PURE__ */ t("input", {
							"aria-label": "Receipt entry",
							inputMode: "numeric",
							value: A,
							onChange: (e) => {
								ee(e.target.value), L.current?.clearReceipt();
							}
						})] })
					]
				}),
				/* @__PURE__ */ t("button", {
					onClick: () => void oe(),
					disabled: j.status !== "ready",
					children: "Inspect receipt"
				}),
				z?.paid === !0 ? /* @__PURE__ */ n("section", {
					"aria-label": "Verified payment receipt",
					children: [
						/* @__PURE__ */ t("h3", { children: "Verified finalized payment" }),
						/* @__PURE__ */ n("dl", {
							className: "wt-facts",
							children: [
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Receipt / corroborating account slot" }), /* @__PURE__ */ n("dd", { children: [
									z.slot,
									" / ",
									z.currentAccountSlot
								] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Principal SOL" }), /* @__PURE__ */ t("dd", { children: X(z.amounts.principalLamports) })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Staking refund SOL" }), /* @__PURE__ */ t("dd", { children: X(z.amounts.stakingRefundLamports) })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "SAT" }), /* @__PURE__ */ t("dd", { children: X(z.amounts.satAtoms) })] }),
								[
									"RecoverSol",
									"StakingRecoverSol",
									"CarryRefundSol",
									"DailyPrizeSol",
									"DailyRefundSol"
								].includes(z.operation) ? /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: z.operation === "DailyPrizeSol" ? "Daily prize SOL" : ["CarryRefundSol", "DailyRefundSol"].includes(z.operation) ? "Contributor SOL refund" : "Recovered SOL" }), /* @__PURE__ */ t("dd", { children: X(z.amounts.recoveredLamports) })] }) : null,
								[
									"ClaimBtc",
									"StakingClaimBtc",
									"CarryClaimBtc",
									"CarryRefundBtc",
									"DailyPrizeBtc",
									"DailyRefundBtc"
								].includes(z.operation) ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "BTC paid by this transaction" }), /* @__PURE__ */ t("dd", { children: x(z.amounts.btcAtoms) })] }), /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Cumulative BTC paid" }), /* @__PURE__ */ t("dd", { children: x(z.cumulativeBtcPaidAtoms) })] })] }) : null,
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "User network fee SOL" }), /* @__PURE__ */ t("dd", { children: X(z.amounts.networkFeeLamports) })] })
							]
						}),
						/* @__PURE__ */ t("code", {
							style: { overflowWrap: "anywhere" },
							children: z.signature
						})
					]
				}) : null,
				z && z.paid === !1 ? /* @__PURE__ */ n("section", {
					"aria-label": "Transaction disposition",
					children: [
						/* @__PURE__ */ t("h3", { children: s[z.status] }),
						/* @__PURE__ */ t("span", {
							className: "wt-tag",
							children: "No payment verified"
						}),
						z.originalTransactionVerified ? /* @__PURE__ */ n("dl", {
							className: "wt-facts",
							children: [
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Signed original" }), /* @__PURE__ */ t("dd", { children: "Verified" })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Transaction / account slot" }), /* @__PURE__ */ n("dd", { children: [
									z.slot,
									" / ",
									z.currentAccountSlot
								] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "User network fee SOL" }), /* @__PURE__ */ t("dd", { children: X(z.amounts.networkFeeLamports) })] })
							]
						}) : /* @__PURE__ */ t("p", { children: "RPC status only. Signature ownership and network fee are unverified." }),
						/* @__PURE__ */ t("code", {
							style: { overflowWrap: "anywhere" },
							children: z.signature
						})
					]
				}) : null
			] }) : null
		]
	});
}
//#endregion
//#region src/draft-preview.mjs
function Ai(e, { now: t = () => Date.now(), onChange: n = () => {} } = {}) {
	let r = 0, i = null, a = !1, o = null, s = {
		status: "idle",
		envelope: null,
		error: null
	}, c = (e) => {
		s = e, a || n(s);
	}, l = (e) => JSON.stringify(e), u = () => s.envelope && t() < s.envelope.expiresAt, d = () => {
		r++, i?.abort(), i = null;
	};
	return {
		getState() {
			return s.envelope && !u() && c({
				status: "expired",
				envelope: null,
				error: "Preview expired. Recalculate before review."
			}), s;
		},
		async calculate(n) {
			d(), o = l(n);
			let s = r, u = o;
			i = new AbortController(), c({
				status: "loading",
				envelope: null,
				error: null
			});
			try {
				let l = await e.preview(n, i.signal);
				if (a || s !== r || u !== o) return;
				if (!Number.isSafeInteger(l.expiresAt) || l.expiresAt <= t()) throw Error("Preview context expired.");
				c({
					status: "ready",
					envelope: l,
					error: null
				});
			} catch (e) {
				!a && s === r && c({
					status: "error",
					envelope: null,
					error: String(e?.message ?? e)
				});
			}
		},
		invalidate() {
			d(), o = null, c({
				status: "idle",
				envelope: null,
				error: null
			});
		},
		async review(n) {
			if (o !== l(n) || s.status !== "ready" || !u()) throw this.invalidate(), Error("Recalculate the current allocation before review.");
			let d = r, f = s.envelope;
			i = new AbortController(), c({
				status: "checking",
				envelope: f,
				error: null
			});
			try {
				if (await e.validate(f, i.signal), a || d !== r || o !== l(n)) throw Error("Draft changed during review.");
				if (t() >= f.expiresAt) throw Error("Preview context expired.");
				return c({
					status: "ready",
					envelope: f,
					error: null
				}), f;
			} catch (e) {
				throw !a && d === r && c({
					status: "error",
					envelope: null,
					error: String(e?.message ?? e)
				}), e;
			}
		},
		dispose() {
			d(), a = !0, o = null, s = {
				status: "idle",
				envelope: null,
				error: null
			};
		}
	};
}
//#endregion
//#region src/useMiningPreview.ts
function ji(e, t) {
	let n = JSON.stringify(t), i = a(null), [s, c] = o({
		key: "",
		status: "idle",
		envelope: null,
		error: null
	});
	r(() => {
		if (!e || !t) return;
		let r = Ai(e, { onChange: (e) => c({
			key: n,
			...e
		}) });
		i.current = r;
		let a = setTimeout(() => void r.calculate(t), 250), o = setInterval(() => r.getState(), 500);
		return () => {
			clearTimeout(a), clearInterval(o), r.dispose(), i.current === r && (i.current = null);
		};
	}, [e, n]);
	let l = s.key === n ? s : {
		status: t ? "loading" : "idle",
		envelope: null,
		error: null
	}, u = () => {
		t && i.current?.calculate(t);
	}, d = async () => {
		if (!t || !i.current) throw Error("Calculate the current draft before review.");
		return i.current.review(t);
	};
	return {
		display: e ? {
			...l,
			refresh: u
		} : void 0,
		review: d
	};
}
//#endregion
//#region src/research.ts
var Mi = /^(0|[1-9][0-9]*)$/, Ni = /^[a-f0-9]{64}$/, Pi = [
	"committed",
	"returned",
	"fees",
	"forfeitures",
	"userCosts",
	"btcSaleSOL",
	"satSaleSOL",
	"btcPending",
	"btcReceived",
	"satEarned",
	"satClaimed",
	"satSold"
];
function Fi(e, t, n = []) {
	if (!e || typeof e != "object" || Array.isArray(e) || t.some((t) => !(t in e)) || Object.keys(e).some((e) => ![...t, ...n].includes(e))) throw Error("Unsupported record fields");
}
function Ii(e, t = 256) {
	if (typeof e != "string" || !e.length || e.length > t) throw Error("Invalid text");
}
function Li(e) {
	if (typeof e != "string" || !Mi.test(e) || e.length > 30) throw Error("Invalid integer amount");
}
function Ri(e) {
	if (typeof e != "string" || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(e) || !Number.isFinite(Date.parse(e)) || new Date(e).toISOString().replace(".000Z", "Z") !== e) throw Error("Invalid UTC time");
}
async function zi(e) {
	return [...new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(e)))].map((e) => e.toString(16).padStart(2, "0")).join("");
}
function Q(e) {
	let t = ti(new TextEncoder().encode(e));
	if (ii(t), t.execution.mandateId !== null || t.execution.mode !== "review") throw Error("Import requires review mode and no mandate. Export an inert strategy first.");
	return t;
}
function Bi(e, t) {
	let n = ti(new TextEncoder().encode(e));
	Ii(t, 80), n.name = t, n.execution = {
		mode: "review",
		runner: "browser",
		mandateId: null
	};
	let r = JSON.stringify(n, null, 2);
	return Q(r), r;
}
function Vi(e) {
	if (new TextEncoder().encode(e).length > 65536) throw Error("History import exceeds 64 KiB");
	let t = ti(new TextEncoder().encode(e));
	if (Fi(t, [
		"schema",
		"source",
		"verified",
		"scope",
		"initialBankroll",
		"skipped",
		"pendingExposure",
		"rows",
		"gaps"
	]), t.schema !== "wen-history-import/1" || t.verified !== !1) throw Error("Imported history must be unverified");
	Ii(t.source), Fi(t.scope, [
		"market",
		"terms",
		"evidence",
		"from",
		"to",
		"dataset",
		"valuation",
		"costs",
		"coverage"
	]);
	for (let e of [
		"market",
		"terms",
		"dataset",
		"valuation",
		"costs",
		"coverage"
	]) Ii(t.scope[e]);
	if (![
		"actual",
		"paper",
		"historical-replay"
	].includes(t.scope.evidence)) throw Error("Invalid evidence class");
	if (Ri(t.scope.from), Ri(t.scope.to), t.scope.from >= t.scope.to) throw Error("Invalid period");
	for (let e of ["initialBankroll", "pendingExposure"]) t[e] !== null && Li(t[e]);
	if (t.skipped !== null && (!Number.isSafeInteger(t.skipped) || t.skipped < 0)) throw Error("Invalid skip count");
	if (!Array.isArray(t.rows) || t.rows.length > 1e3 || !Array.isArray(t.gaps) || t.gaps.length > 1e3) throw Error("Record capacity exceeded");
	t.gaps.forEach((e) => Ii(e));
	for (let e of t.rows) {
		if (Fi(e, [
			"id",
			"cursor",
			"strategyDigest",
			"at",
			"finality",
			"category",
			...Pi
		]), Ii(e.id), !Number.isSafeInteger(e.cursor) || e.cursor < 1 || !Ni.test(e.strategyDigest)) throw Error("Invalid record identity");
		if (Ri(e.at), e.at < t.scope.from || e.at >= t.scope.to) throw Error("History lies outside declared period");
		if (![
			"provisional",
			"finalized",
			"reverted"
		].includes(e.finality) || ![
			"mining",
			"staking",
			"prize",
			"carry",
			"transfer"
		].includes(e.category)) throw Error("Invalid history state");
		Pi.forEach((t) => Li(e[t]));
	}
	return Hi(t.rows), t;
}
function Hi(e) {
	let t = /* @__PURE__ */ new Map();
	for (let n of [...e].sort((e, t) => e.cursor - t.cursor)) {
		let e = t.get(n.id);
		if (e && e.cursor === n.cursor && JSON.stringify(e) !== JSON.stringify(n)) throw Error("Conflicting history event");
		(!e || n.cursor > e.cursor) && t.set(n.id, n);
	}
	return [...t.values()].sort((e, t) => e.at.localeCompare(t.at) || e.id.localeCompare(t.id));
}
function Ui(e, t) {
	let n = 0n, r = 0n, i = 0n, a = 0n, o = 0n, s = 0n, c = 0n, l = 0n, u = 0n, d = 0n, f = 0n, p = 0n, m = 0n, h = 0n, g = 0n, _ = 0, v = 0, y = {};
	for (let b of Hi(e.rows).filter((e) => e.strategyDigest === t)) if (b.finality !== "reverted") {
		if (b.finality !== "finalized") {
			v++;
			continue;
		}
		if (b.category !== "mining") {
			y[b.category] = String(BigInt(y[b.category] ?? "0") + BigInt(b.returned));
			continue;
		}
		_++, n += BigInt(b.committed), r += BigInt(b.returned), i += BigInt(b.fees), a += BigInt(b.forfeitures), o += BigInt(b.userCosts), s += BigInt(b.btcSaleSOL), c += BigInt(b.satSaleSOL), l += BigInt(b.btcReceived), u += BigInt(b.btcPending), d += BigInt(b.satEarned), f += BigInt(b.satClaimed), p += BigInt(b.satSold), m += BigInt(b.returned) - BigInt(b.committed) - BigInt(b.userCosts) + BigInt(b.btcSaleSOL) + BigInt(b.satSaleSOL), m > h && (h = m), h - m > g && (g = h - m);
	}
	return {
		entries: _,
		unsettled: v,
		committed: String(n),
		returned: String(r),
		fees: String(i),
		forfeitures: String(a),
		userCosts: String(o),
		btcReceived: String(l),
		btcPending: String(u),
		satEarned: String(d),
		satClaimed: String(f),
		satSold: String(p),
		btcSaleSOL: String(s),
		satSaleSOL: String(c),
		netSOL: String(m),
		drawdown: String(g),
		costPerSAT: f === 0n ? null : {
			numerator: String(i + a + o - s),
			denominator: String(f)
		},
		perTurnover: n === 0n ? null : {
			numerator: String(m),
			denominator: String(n)
		},
		overlays: y
	};
}
function Wi(e) {
	if (e.length < 2 || e.length > 4) throw Error("Choose 2–4 strategies");
	let t = Object.keys(e[0].scope).filter((t) => e.some((n) => n.scope[t] !== e[0].scope[t]));
	return {
		differences: t,
		rankable: !1,
		reason: t.length ? "Different comparison scopes" : e.some((e) => e.gaps.length) ? "Incomplete observation coverage" : "Imported evidence is unverified; descriptive comparison only"
	};
}
function $(e, t = 9) {
	let n = BigInt(e), r = n < 0n ? "-" : "", i = n < 0n ? -n : n;
	return r + (i / 10n ** BigInt(t)).toString() + ("." + (i % 10n ** BigInt(t)).toString().padStart(t, "0")).replace(/\.?0+$/, "");
}
function Gi(e) {
	let t = "wen-research-v1:" + e, n = async () => {
		let e = localStorage.getItem(t);
		if (!e) return {
			revision: 0,
			versions: [],
			bundles: []
		};
		let n = JSON.parse(e);
		if (!Number.isSafeInteger(n.revision) || !Array.isArray(n.versions) || !Array.isArray(n.bundles)) throw Error("Invalid local library; export and preserve storage");
		for (let e of n.versions) if (Q(e.raw), await zi(e.raw) !== e.digest) throw Error("Library digest mismatch");
		return n.bundles.forEach((e) => Vi(JSON.stringify(e))), n;
	}, r = async (e, r) => {
		if (!navigator.locks) throw Error("Safe browser storage locking unavailable");
		return navigator.locks.request(t, async () => {
			let i = await n();
			if (i.revision !== e) throw Error("Library changed; reload before saving");
			if (await r(i) === !1) return i;
			i.revision++;
			let a = JSON.stringify(i);
			if (a.length > 2e6) throw Error("Local library capacity reached; export records");
			return localStorage.setItem(t, a), i;
		});
	};
	return {
		label: "Private browser storage · export a backup",
		load: n,
		save: (e, t) => r(e, async (e) => {
			let n = Q(t), r = await zi(t);
			if (e.versions.some((e) => e.digest === r)) return !1;
			if (e.versions.length >= 100) throw Error("100-version capacity reached");
			e.versions.push({
				digest: r,
				raw: t,
				name: n.name,
				createdAt: (/* @__PURE__ */ new Date()).toISOString(),
				origin: "owner-import"
			});
		}),
		importHistory: (e, t) => r(e, async (e) => {
			let n = Vi(t);
			if (e.bundles.length >= 50) throw Error("50-bundle capacity reached");
			e.bundles.push(n);
		})
	};
}
//#endregion
//#region src/SharedResearch.tsx
function Ki(e) {
	let t = e;
	return t?.schema === "wen-research-chat-review/1" && Object.keys(t).length === 6 && Object.keys(t).every((e) => [
		"schema",
		"profileId",
		"revision",
		"expiresAt",
		"text",
		"source"
	].includes(e)) && typeof t.profileId == "string" && t.profileId.length > 0 && t.profileId.length <= 80 && Number.isSafeInteger(t.revision) && t.revision >= 0 && Number.isSafeInteger(t.expiresAt) && t.expiresAt * 1e3 > Date.now() && t.expiresAt * 1e3 <= Date.now() + 12e4 && typeof t.text == "string" && new TextEncoder().encode(t.text).length <= 4e3 && t.text.length > 0 && typeof t.source == "string" && /^wen-research-review-v1:[a-f0-9]{64}$/.test(t.source) ? t : null;
}
async function qi(e) {
	let t = await fetch(`/api/mining/v1/research?lookback=${e}`, {
		cache: "no-store",
		credentials: "omit",
		signal: AbortSignal.timeout(4e3)
	});
	if (!t.ok) throw Error("Research service unavailable");
	let n = await t.text();
	if (n.length > 262144) throw Error("Research response too large");
	return JSON.parse(n);
}
function Ji(e) {
	if (!e || e.schema !== "wen-research-data/1" || e.execution !== "disabled" || e.finality !== "research" || e.method !== "wen-kraken-research/1" || typeof e.sourceCursor != "string" || !/^\d{1,20}$/.test(e.sourceCursor) || !Number.isFinite(Date.parse(e.generatedAt)) || Math.abs(Date.now() - Date.parse(e.generatedAt)) > 1e4) throw Error("Unsupported or stale research snapshot");
	let t = e.data;
	if (!t || t.method !== e.method || t.authoritative !== !1 || ![
		"valid",
		"stale",
		"invalid"
	].includes(t.quality) || !Array.isArray(t.candles) || t.candles.length > 1441 || !Array.isArray(t.observationIds) || t.observationIds.length > 1442 || !/^[a-f0-9]{64}$/.test(t.inputDigest) || !t.candles.every((e) => Number.isFinite(Date.parse(e.at)) && Number.isFinite(e.midpoint) && e.midpoint > 0) || !(t.quote === null || Number.isFinite(t.quote) && t.quote > 0) || !(t.metrics === null || typeof t.metrics?.returnBps == "string" && /^-?\d+$/.test(t.metrics.returnBps) && typeof t.metrics.realizedVolatilityBps == "string" && /^\d+$/.test(t.metrics.realizedVolatilityBps))) throw Error("Invalid research data");
	if (t.quote !== null && (!Number.isFinite(Date.parse(t.quoteEventAt)) || !Number.isFinite(Date.parse(t.quoteReceivedAt)) || Date.parse(t.quoteEventAt) > Date.now() + 1e3 || Date.parse(t.quoteReceivedAt) > Date.now() + 1e3)) throw Error("Missing quote time");
	return e;
}
function Yi({ read: i = qi, onDiscuss: a }) {
	let [s, c] = o(!1), [l, u] = o(1), [d, f] = o(null), [p, m] = o(""), [h, g] = o(Date.now());
	r(() => {
		if (!s) return;
		let e = !0, t, n = async () => {
			try {
				let t = Ji(await i(l));
				e && (f(t), m(""));
			} catch (t) {
				e && (f(null), m(t.message));
			} finally {
				e && (t = setTimeout(n, 5e3));
			}
		};
		return f(null), n(), () => {
			e = !1, clearTimeout(t);
		};
	}, [
		s,
		l,
		i
	]), r(() => {
		if (!s) return;
		let e = setInterval(() => g(Date.now()), 500);
		return () => clearInterval(e);
	}, [s]);
	let _ = d?.data, v = _ && _.quality === "valid" && h - Date.parse(_.quoteEventAt) <= 5e3 && h - Date.parse(_.quoteReceivedAt) <= 5e3 && h - Date.parse(d.generatedAt) <= 1e4, y = Ki(d?.chatContext), b = _?.candles ?? [], x = Math.min(...b.map((e) => e.midpoint)), S = Math.max(...b.map((e) => e.midpoint)) - x || 1;
	return /* @__PURE__ */ n("section", {
		className: "wr-panel",
		"aria-label": "Shared Kraken research",
		children: [
			/* @__PURE__ */ t("h3", { children: "Kraken BTC/USD" }),
			/* @__PURE__ */ t("button", {
				onClick: () => {
					c((e) => !e), f(null), m("");
				},
				children: s ? "Disconnect research" : "Connect research"
			}),
			s && /* @__PURE__ */ n(e, { children: [
				/* @__PURE__ */ t("nav", {
					"aria-label": "Shared research lookback",
					children: [
						1,
						30,
						60,
						240,
						720,
						1440
					].map((e) => /* @__PURE__ */ n("button", {
						"aria-pressed": l === e,
						onClick: () => u(e),
						children: [e, "m"]
					}, e))
				}),
				p && /* @__PURE__ */ t("p", {
					role: "status",
					children: p
				}),
				!d && !p && /* @__PURE__ */ t("p", { children: "Connecting…" }),
				d && /* @__PURE__ */ n(e, { children: [
					/* @__PURE__ */ t("span", {
						className: "wt-badge",
						children: "Research · execution disabled"
					}),
					/* @__PURE__ */ n("dl", { children: [
						/* @__PURE__ */ t("dt", { children: "Feed" }),
						/* @__PURE__ */ n("dd", { children: [
							d.connection,
							" · ",
							v ? "fresh" : "stale"
						] }),
						/* @__PURE__ */ t("dt", { children: "BTC/USD midpoint" }),
						/* @__PURE__ */ t("dd", {
							"data-testid": "shared-quote",
							children: v && _.quote !== null ? _.quote.toFixed(2) : "Unavailable"
						}),
						/* @__PURE__ */ t("dt", { children: "Return / realized volatility" }),
						/* @__PURE__ */ t("dd", { children: v && _.metrics ? `${_.metrics.returnBps} bps / ${_.metrics.realizedVolatilityBps} bps` : "Unavailable · incomplete or stale data" }),
						/* @__PURE__ */ t("dt", { children: "Late updates skipped (3m)" }),
						/* @__PURE__ */ t("dd", { children: _.ignoredLateObservations ?? 0 }),
						/* @__PURE__ */ t("dt", { children: "Observed at" }),
						/* @__PURE__ */ t("dd", { children: _.quoteEventAt ?? "Unavailable" }),
						/* @__PURE__ */ t("dt", { children: "Source cursor" }),
						/* @__PURE__ */ t("dd", {
							"data-testid": "shared-cursor",
							children: d.sourceCursor
						}),
						/* @__PURE__ */ t("dt", { children: "Input digest" }),
						/* @__PURE__ */ t("dd", { children: /* @__PURE__ */ t("code", {
							"data-testid": "shared-digest",
							children: _.inputDigest
						}) })
					] }),
					b.length > 0 && /* @__PURE__ */ t("svg", {
						viewBox: "0 0 700 160",
						role: "img",
						"aria-label": "Shared BTC minute closes",
						style: {
							width: "100%",
							height: 160
						},
						children: b.map((e, r) => /* @__PURE__ */ t("circle", {
							cx: 20 + (Date.parse(e.at) - Date.parse(b[0].at)) / Math.max(1, Date.parse(b.at(-1).at) - Date.parse(b[0].at)) * 660,
							cy: 140 - (e.midpoint - x) / S * 120,
							r: 3,
							fill: "#27C5DA",
							children: /* @__PURE__ */ n("title", { children: [
								e.at,
								": ",
								e.midpoint
							] })
						}, e.at))
					}),
					/* @__PURE__ */ t("button", {
						onClick: () => {
							let { chatContext: e, ...t } = d, n = document.createElement("a"), r = URL.createObjectURL(new Blob([JSON.stringify(t, null, 2)], { type: "application/json" }));
							n.href = r, n.download = "kraken-research.json", n.click(), setTimeout(() => URL.revokeObjectURL(r), 1e3);
						},
						children: "Export shared snapshot"
					}),
					a && /* @__PURE__ */ t("button", {
						disabled: !v || !y,
						onClick: () => {
							v && y && a(structuredClone(y));
						},
						children: "Discuss research"
					})
				] })
			] })
		]
	});
}
//#endregion
//#region src/capture-core.mjs
var Xi = class {
	constructor({ source: e = "Coinbase Exchange BTC-USD imported capture", method: t = "wen-research/1", requireHeartbeat: n = !0, dropLate: r = !1 } = {}) {
		this.source = e, this.method = t, this.requireHeartbeat = n, this.dropLate = r, this.late = [], this.events = [], this.lastByType = {
			ticker: -1n,
			heartbeat: -1n
		}, this.generation = 0, this.connected = !1, this.fault = null, this.gaps = [], this.gapStart = null;
	}
	connect(e) {
		e && this.gapStart === null && (this.gapStart = Math.floor(Date.parse(e) / 6e4) * 6e4), this.connected = !0, this.fault = null, this.generation++;
	}
	disconnect(e) {
		e && this.gapStart === null && (this.gapStart = Date.parse(e)), this.connected = !1;
	}
	ingest(e) {
		if (!this.connected) throw Error("capture disconnected");
		if (!e || !["ticker", "heartbeat"].includes(e.type) || e.product_id !== "BTC-USD" || !/^\d+$/.test(String(e.sequence)) || !Number.isFinite(Date.parse(e.time)) || !Number.isFinite(Date.parse(e.receivedAt))) throw Error("invalid capture event");
		let t = BigInt(e.sequence);
		if (t <= this.lastByType[e.type]) return !1;
		if (e.type === "ticker" && (!Number.isFinite(Number(e.best_bid)) || !Number.isFinite(Number(e.best_ask)) || Number(e.best_bid) <= 0 || Number(e.best_ask) < Number(e.best_bid))) throw Error("invalid bid/ask");
		let n = this.events.at(-1);
		if (n && n.generation === this.generation && Date.parse(e.time) < Date.parse(n.time)) return this.dropLate ? (this.late.push({
			at: Date.parse(e.receivedAt),
			id: `${this.generation}:ticker:${e.sequence}`
		}), this.late = this.late.filter((t) => t.at >= Date.parse(e.receivedAt) - 18e4), !1) : (this.fault = "out-of-order exchange time", !1);
		if (e.type === "ticker" && this.gapStart !== null) {
			let t = Date.parse(e.receivedAt);
			t > this.gapStart && this.gaps.push([this.gapStart, t]), this.gapStart = null;
		}
		this.lastByType[e.type] = t, this.events.push({
			...e,
			sequence: String(t),
			generation: this.generation
		});
		let r = Date.parse(e.receivedAt) - 1728e5;
		return this.events = this.events.filter((e) => Date.parse(e.receivedAt) >= r), this.events.length > 2e5 && (this.fault = "capture capacity exceeded", this.events.shift()), !0;
	}
	snapshot(e) {
		let t = Date.parse(e);
		if (!Number.isFinite(t)) throw Error("invalid evaluation time");
		let n = this.events.filter((e) => Date.parse(e.time) <= t && Date.parse(e.receivedAt) <= t), r = n.filter((e) => e.type === "ticker").at(-1), i = n.filter((e) => e.type === "heartbeat").at(-1), a = this.connected && !this.fault && r && (!this.requireHeartbeat || i) && r.generation === this.generation && (!this.requireHeartbeat || i.generation === this.generation) && t - Date.parse(r.receivedAt) <= 5e3 && t - Date.parse(r.time) <= 5e3 && (!this.requireHeartbeat || t - Date.parse(i.receivedAt) <= 5e3 && t - Date.parse(i.time) <= 5e3), o = /* @__PURE__ */ new Map();
		for (let e of n.filter((e) => e.type === "ticker")) {
			let n = Math.floor(Date.parse(e.time) / 6e4) * 6e4;
			n + 6e4 > t || (this.gapStart === null ? this.gaps : [...this.gaps, [this.gapStart, t]]).some(([e, t]) => n < t && n + 6e4 > e) || o.set(n, {
				at: new Date(n + 6e4).toISOString(),
				midpoint: (Number(e.best_bid) + Number(e.best_ask)) / 2,
				sourceIds: [`${e.generation}:ticker:${e.sequence}`]
			});
		}
		let s = [...o.values()];
		return {
			source: this.source,
			method: this.method,
			at: e,
			quality: a ? "valid" : this.fault ? "invalid" : "stale",
			quote: a ? (Number(r.best_bid) + Number(r.best_ask)) / 2 : null,
			candles: s,
			observationIds: n.map((e) => `${e.generation}:${e.type}:${e.sequence}`),
			authoritative: !1,
			ignoredLateObservations: this.late.filter((e) => e.at <= t && e.at >= t - 18e4).length
		};
	}
	metrics(e, t, n = []) {
		if (![
			1,
			30,
			60,
			240,
			720,
			1440
		].includes(t)) throw Error("unsupported lookback");
		let r = this.snapshot(e), i = Math.floor(Date.parse(e) / 6e4) * 6e4;
		if (n.length) {
			let t = new Map(n.map((e) => [e.at, e]));
			for (let e of r.candles) t.set(e.at, e);
			let i = this.gapStart === null ? this.gaps : [...this.gaps, [this.gapStart, Date.parse(e)]];
			r.candles = [...t.values()].filter((e) => !i.some(([t, n]) => Date.parse(e.at) - 6e4 < n && Date.parse(e.at) > t)).sort((e, t) => Date.parse(e.at) - Date.parse(t.at));
		}
		let a = Array.from({ length: t + 1 }, (e, n) => r.candles.find((e) => Date.parse(e.at) === i - (t - n) * 6e4));
		if (r.quality !== "valid" || a.some((e) => !e) || Date.parse(e) - i > 9e4) return {
			...r,
			metrics: null,
			reason: r.quality === "valid" ? "Incomplete minute history" : "Fresh quote unavailable"
		};
		let o = a.map((e) => e.midpoint), s = (e) => Math.sign(e) * Math.floor(Math.abs(e) + .5);
		return {
			...r,
			metrics: {
				returnBps: String(s(1e4 * (o.at(-1) / o[0] - 1))),
				realizedVolatilityBps: String(s(1e4 * Math.sqrt(o.slice(1).reduce((e, t, n) => e + Math.log(t / o[n]) ** 2, 0))))
			},
			lookbackMinutes: t
		};
	}
};
//#endregion
//#region src/kraken-wire.mjs
function Zi(e, t) {
	if (typeof e != "string" || new TextEncoder().encode(e).length > 65536) throw Error("Kraken frame too large");
	return t(e.replace(/"(?:[^"\\]|\\.)*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g, (e) => e.startsWith("\"") ? e : JSON.stringify(e)));
}
function Qi(e, t) {
	let n = Zi(e, t);
	if (n.channel === "heartbeat" || n.channel === "status" || n.method === "subscribe") {
		if (n.success === !1 || n.channel === "status" && n.data?.[0]?.system !== "online") throw Error("Kraken unavailable or subscription refused");
		return null;
	}
	if (n.channel !== "ticker" || !["snapshot", "update"].includes(n.type) || !Array.isArray(n.data) || n.data.length !== 1) throw Error("Unsupported Kraken message");
	let r = n.data[0];
	if (r.symbol !== "BTC/USD" || ![r.bid, r.ask].every((e) => typeof e == "string" && /^\d{1,9}(\.\d{1,6})?$/.test(e) && Number(e) > 0) || Number(r.ask) < Number(r.bid)) throw Error("Invalid Kraken instrument or BBO");
	if (typeof r.timestamp != "string" || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,6})?Z$/.test(r.timestamp) || !Number.isFinite(Date.parse(r.timestamp)) || new Date(r.timestamp).toISOString().slice(0, 19) !== r.timestamp.slice(0, 19)) throw Error("Invalid Kraken source time");
	return {
		time: r.timestamp,
		best_bid: r.bid,
		best_ask: r.ask
	};
}
function $i(e, t) {
	if (!e || Array.isArray(e) || typeof e != "object" || Object.keys(e).length !== t.length || t.some((t) => !Object.hasOwn(e, t))) throw Error("Unexpected capture fields");
}
function ea(e) {
	if (typeof e != "string" || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,6})?Z$/.test(e) || !Number.isFinite(Date.parse(e)) || new Date(e).toISOString().slice(0, 19) !== e.slice(0, 19)) throw Error("Invalid capture UTC time");
	return Date.parse(e);
}
async function ta(e, t) {
	if (new TextEncoder().encode(e).length > 2097152) throw Error("Capture exceeds 2 MiB");
	let n = e.split(/\r?\n/);
	if (n.at(-1) === "" && n.pop(), n.length < 2 || n.length > 10001 || n.some((e) => !e.trim())) throw Error("Use one header and 1–10,000 NDJSON events");
	let r = (e) => ti(new TextEncoder().encode(e)), i = r(n[0]);
	if ($i(i, [
		"schema",
		"source",
		"evaluatedAt",
		"lookbackMinutes"
	]), !["wen-research-capture/1", "wen-kraken-capture/1"].includes(i.schema) || typeof i.source != "string" || i.source.length < 1 || i.source.length > 256) throw Error("Invalid capture header");
	let a = ea(i.evaluatedAt), o = t ?? i.lookbackMinutes;
	if (![
		1,
		30,
		60,
		240,
		720,
		1440
	].includes(i.lookbackMinutes) || ![
		1,
		30,
		60,
		240,
		720,
		1440
	].includes(o)) throw Error("Unsupported lookback");
	let s = i.schema === "wen-kraken-capture/1", c = new Xi(s ? {
		source: "Kraken BTC/USD BBO imported capture",
		method: "wen-kraken-research/1",
		requireHeartbeat: !1,
		dropLate: !0
	} : {}), l = 0, u = -Infinity, d = !1, f = /* @__PURE__ */ new Map(), p = 0;
	for (let e of n.slice(1)) {
		let t = r(e);
		if (l++, s && Object.hasOwn(t, "raw")) {
			$i(t, ["receivedAt", "raw"]);
			let e = ea(t.receivedAt);
			if (!d || e < u || e > a || typeof t.raw != "string") throw Error("Invalid Kraken arrival or connection");
			u = e;
			let n = Qi(t.raw, r);
			if (!n) continue;
			t = {
				type: "ticker",
				product_id: "BTC-USD",
				sequence: String(l),
				time: n.time,
				receivedAt: t.receivedAt,
				best_bid: n.best_bid,
				best_ask: n.best_ask
			};
		} else if (s && t.type !== "connect" && t.type !== "disconnect") throw Error("Use original Kraken raw records");
		if (t.type === "connect" || t.type === "disconnect") {
			$i(t, ["type", "receivedAt"]);
			let e = ea(t.receivedAt);
			if (e < u || e > a) throw Error("Capture arrival order or future control is invalid");
			if (u = e, t.type === "connect") {
				if (d) throw Error("Duplicate connection");
				c.connect(t.receivedAt), d = !0, p++;
			} else {
				if (!d) throw Error("Already disconnected");
				c.disconnect(t.receivedAt), d = !1;
			}
			continue;
		}
		let n = [
			"type",
			"product_id",
			"sequence",
			"time",
			"receivedAt"
		];
		if (t.type === "ticker") n.push("best_bid", "best_ask");
		else if (t.type !== "heartbeat") throw Error("Unsupported capture event");
		$i(t, n);
		let i = ea(t.receivedAt), o = ea(t.time);
		if (i < u || i > a || o > a) throw Error("Capture arrival order or future observation is invalid");
		if (u = i, typeof t.sequence != "string" || !/^(0|[1-9]\d{0,19})$/.test(t.sequence)) throw Error("Use an exact decimal sequence string");
		if (t.type === "ticker" && [t.best_bid, t.best_ask].some((e) => typeof e != "string" || !/^\d{1,9}(\.\d{1,6})?$/.test(e) || Number(e) <= 0)) throw Error("Invalid decimal quote");
		let m = `${p}:${t.type}:${t.sequence}`, h = JSON.stringify(n.map((e) => t[e]));
		if (f.has(m) && f.get(m) !== h) throw Error("Conflicting capture identity");
		f.set(m, h), c.ingest(t);
	}
	let m = c.metrics(i.evaluatedAt, o);
	s && (m.observationIds = m.observationIds.map((e) => "record:" + e), m.candles = m.candles.map((e) => ({
		...e,
		sourceIds: e.sourceIds.map((e) => "record:" + e)
	})));
	let h = Math.floor(a / 6e4) * 6e4, g = new Set(m.candles.map((e) => Date.parse(e.at))), _ = Array.from({ length: o + 1 }, (e, t) => h - (o - t) * 6e4).filter((e) => !g.has(e)).map((e) => new Date(e).toISOString());
	return {
		raw: e,
		captureSHA256: await zi(e),
		source: i.source,
		evaluatedAt: i.evaluatedAt,
		lookbackMinutes: o,
		status: "imported-unverified",
		summary: m,
		eventCount: n.length - 1,
		missingMinuteCloses: _
	};
}
function na(e) {
	let { raw: t, ...n } = e;
	return JSON.stringify({
		schema: "wen-research-snapshot/1",
		execution: "disabled",
		...n
	}, null, 2);
}
//#endregion
//#region src/CaptureResearch.tsx
function ra(e, t) {
	let n = URL.createObjectURL(new Blob([t], { type: "application/json" })), r = document.createElement("a");
	r.href = n, r.download = e, r.click(), setTimeout(() => URL.revokeObjectURL(n), 1e3);
}
function ia() {
	let [r, i] = o(""), [s, c] = o(null), [l, u] = o(""), [d, f] = o(!1), p = a(0);
	async function m(e) {
		let t = ++p.current;
		f(!0), u(""), c(null);
		try {
			let n = await ta(r, e);
			t === p.current && c(n);
		} catch (e) {
			t === p.current && u(e.message);
		} finally {
			t === p.current && f(!1);
		}
	}
	function h(e) {
		p.current++, f(!1), i(e), c(null), u("");
	}
	let g = s?.summary.candles ?? [], _ = g.map((e) => e.midpoint), v = Math.min(..._), y = Math.max(..._), b = y - v || 1, x = Date.parse(g[0]?.at ?? ""), S = Date.parse(g.at(-1)?.at ?? "") - x || 1;
	return /* @__PURE__ */ n("div", {
		className: "wr-panel",
		"aria-label": "Captured BTC research",
		children: [
			/* @__PURE__ */ t("h3", { children: "Historical capture" }),
			/* @__PURE__ */ n("label", { children: ["BTC capture NDJSON", /* @__PURE__ */ t("textarea", {
				"aria-label": "BTC capture NDJSON",
				value: r,
				onChange: (e) => h(e.target.value),
				spellCheck: !1
			})] }),
			/* @__PURE__ */ n("label", { children: ["Import BTC capture", /* @__PURE__ */ t("input", {
				type: "file",
				accept: ".ndjson,.jsonl,application/x-ndjson",
				onChange: async (e) => {
					let t = e.target.files?.[0];
					if (!t) return;
					let n = ++p.current;
					if (c(null), t.size > 2097152) {
						u("Capture exceeds 2 MiB");
						return;
					}
					let r = await t.text();
					n === p.current && h(r);
				}
			})] }),
			/* @__PURE__ */ t("button", {
				disabled: !r || d,
				onClick: () => void m(),
				children: "Inspect capture"
			}),
			/* @__PURE__ */ n("details", { children: [/* @__PURE__ */ t("summary", { children: "Capture format" }), /* @__PURE__ */ t("p", { children: "NDJSON: one wen-research-capture/1 header (source, evaluatedAt, lookbackMinutes), then connect/disconnect records and Coinbase BTC-USD ticker/heartbeat records. Sequence and bid/ask values are exact strings. See the package capture contract. This tab is transient; export the original to retain it. Kraken imports use wen-kraken-capture/1 with original raw WebSocket messages and receive times." })] }),
			l && /* @__PURE__ */ t("p", {
				role: "alert",
				children: l
			}),
			s && /* @__PURE__ */ n(e, { children: [
				/* @__PURE__ */ t("p", {
					className: "wt-badge",
					children: "Historical · unverified · execution disabled"
				}),
				/* @__PURE__ */ n("dl", { children: [
					/* @__PURE__ */ t("dt", { children: "Source / evaluated at" }),
					/* @__PURE__ */ n("dd", { children: [
						s.source,
						" · ",
						s.evaluatedAt
					] }),
					/* @__PURE__ */ t("dt", { children: "Feed / method" }),
					/* @__PURE__ */ n("dd", { children: [
						s.summary.source,
						" · ",
						s.summary.method
					] }),
					/* @__PURE__ */ t("dt", { children: "Capture SHA-256" }),
					/* @__PURE__ */ t("dd", { children: /* @__PURE__ */ t("code", { children: s.captureSHA256 }) }),
					/* @__PURE__ */ t("dt", { children: "Quality at recorded time" }),
					/* @__PURE__ */ t("dd", { children: s.summary.quality }),
					/* @__PURE__ */ t("dt", { children: "Lookback" }),
					/* @__PURE__ */ n("dd", { children: [s.lookbackMinutes, " minutes"] }),
					/* @__PURE__ */ t("dt", { children: "Return / realized volatility" }),
					/* @__PURE__ */ t("dd", { children: s.summary.metrics ? `${s.summary.metrics.returnBps} bps / ${s.summary.metrics.realizedVolatilityBps} bps` : `Unavailable · ${s.summary.reason}` }),
					/* @__PURE__ */ t("dt", { children: "Missing minute closes" }),
					/* @__PURE__ */ t("dd", { children: s.missingMinuteCloses.length })
				] }),
				/* @__PURE__ */ t("nav", {
					"aria-label": "Capture lookback",
					children: [
						1,
						30,
						60,
						240,
						720,
						1440
					].map((e) => /* @__PURE__ */ n("button", {
						"aria-pressed": s.lookbackMinutes === e,
						onClick: () => void m(e),
						children: [e, "m"]
					}, e))
				}),
				g.length > 0 && /* @__PURE__ */ n("svg", {
					viewBox: "0 0 700 200",
					role: "img",
					"aria-label": "Historical BTC midpoint closes",
					style: {
						width: "100%",
						height: "200px",
						background: "#15141c"
					},
					children: [
						/* @__PURE__ */ t("title", { children: "Recorded completed-minute midpoint closes; gaps are not connected" }),
						g.map((e, r) => {
							let i = 20 + (Date.parse(e.at) - x) / S * 660, a = 170 - (e.midpoint - v) / b * 140;
							return /* @__PURE__ */ t("circle", {
								cx: i,
								cy: a,
								r: "3",
								fill: "#27C5DA",
								children: /* @__PURE__ */ n("title", { children: [
									e.at,
									": ",
									e.midpoint,
									" USD"
								] })
							}, e.at);
						}),
						/* @__PURE__ */ n("text", {
							x: "20",
							y: "15",
							fill: "#B7B2C8",
							children: [y.toFixed(2), " USD"]
						}),
						/* @__PURE__ */ n("text", {
							x: "20",
							y: "195",
							fill: "#B7B2C8",
							children: [v.toFixed(2), " USD"]
						})
					]
				}),
				/* @__PURE__ */ t("button", {
					onClick: () => ra("btc-capture.ndjson", s.raw),
					children: "Export original capture"
				}),
				/* @__PURE__ */ t("button", {
					onClick: () => ra("btc-research-snapshot.json", na(s)),
					children: "Export research snapshot"
				}),
				/* @__PURE__ */ n("details", { children: [
					/* @__PURE__ */ t("summary", { children: "Observation provenance and gaps" }),
					/* @__PURE__ */ n("p", { children: [
						s.eventCount,
						" imported records · ",
						s.summary.method
					] }),
					/* @__PURE__ */ t("p", { children: s.missingMinuteCloses.join(", ") || "No missing closes in selected lookback" }),
					/* @__PURE__ */ t("div", {
						className: "wr-table",
						children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
							/* @__PURE__ */ t("th", { children: "Minute close UTC" }),
							/* @__PURE__ */ t("th", { children: "BTC midpoint USD" }),
							/* @__PURE__ */ t("th", { children: "Observation" })
						] }) }), /* @__PURE__ */ t("tbody", { children: g.map((e) => /* @__PURE__ */ n("tr", { children: [
							/* @__PURE__ */ t("td", { children: e.at }),
							/* @__PURE__ */ t("td", { children: e.midpoint }),
							/* @__PURE__ */ t("td", { children: e.sourceIds.join(", ") })
						] }, e.at)) })] })
					})
				] })
			] })
		]
	});
}
//#endregion
//#region src/catalogue.ts
var aa = (e, t) => e < t ? -1 : +(e > t);
function oa(e, t) {
	let n = /* @__PURE__ */ new Map();
	for (let e of t) for (let t of Hi(e.rows)) {
		if (t.finality === "reverted") continue;
		let r = n.get(t.strategyDigest) ?? /* @__PURE__ */ new Set();
		r.add(e.scope.evidence), n.set(t.strategyDigest, r);
	}
	return [...new Map(e.map((e) => [e.digest, e])).values()].map((e) => {
		let t = Q(e.raw);
		return {
			version: e,
			document: t,
			name: t.name,
			asset: t.market.assetMint,
			thesis: t.research.thesis,
			horizonMinutes: t.research.horizonMinutes,
			evidence: [...n.get(e.digest) ?? []].sort(aa)
		};
	}).sort((e, t) => (Date.parse(t.version.createdAt) || 0) - (Date.parse(e.version.createdAt) || 0) || aa(e.version.digest, t.version.digest));
}
function sa(e, t) {
	let n = (t.query ?? "").trim().toLowerCase().split(/\s+/).filter(Boolean);
	return e.filter((e) => {
		let r = [
			e.name,
			e.document.description,
			e.version.digest,
			e.asset,
			e.thesis,
			e.horizonMinutes,
			e.version.origin,
			...e.evidence
		].join(" ").toLowerCase();
		return n.every((e) => r.includes(e)) && (!t.asset || t.asset === e.asset) && (!t.thesis || t.thesis === e.thesis) && (!t.horizon || t.horizon === String(e.horizonMinutes)) && (!t.origin || t.origin === e.version.origin) && (!t.evidence || (t.evidence === "none" ? e.evidence.length === 0 : e.evidence.includes(t.evidence)));
	});
}
//#endregion
//#region src/StrategyLibrary.tsx
function ca({ library: r, builtins: a, selected: s, inspect: c, compare: l, exportVersion: u, discuss: d, busy: f = !1 }) {
	let [p, m] = o("Built-in"), [h, g] = o({}), _ = i(() => oa(p === "Built-in" ? a : r.versions, r.bundles), [
		p,
		a,
		r
	]), v = sa(_, h), y = (e) => [...new Set(_.map((t) => String(t[e])))].sort();
	return /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("nav", {
		"aria-label": "Library collections",
		children: [
			"Built-in",
			"Mine",
			"Following",
			"Public"
		].map((e) => /* @__PURE__ */ t("button", {
			"aria-pressed": p === e,
			onClick: () => {
				m(e), g({});
			},
			children: e
		}, e))
	}), ["Following", "Public"].includes(p) ? /* @__PURE__ */ t("p", { children: "Public discovery is not connected. Private strategies are never published by saving." }) : /* @__PURE__ */ n(e, { children: [
		/* @__PURE__ */ n("div", {
			className: "wr-filters",
			role: "group",
			"aria-label": "Library filters",
			children: [
				/* @__PURE__ */ n("label", {
					className: "wr-search",
					children: ["Search strategies", /* @__PURE__ */ t("input", {
						type: "search",
						maxLength: 160,
						value: h.query ?? "",
						onChange: (e) => g((t) => ({
							...t,
							query: e.target.value
						}))
					})]
				}),
				[
					[
						"asset",
						"Asset mint",
						y("asset")
					],
					[
						"thesis",
						"Thesis",
						y("thesis")
					],
					[
						"horizon",
						"Horizon (minutes)",
						y("horizonMinutes")
					],
					[
						"evidence",
						"History evidence",
						[
							"none",
							"paper",
							"historical-replay",
							"actual"
						]
					],
					[
						"origin",
						"Record source",
						[...new Set(_.map((e) => e.version.origin))].sort()
					]
				].map(([e, r, i]) => /* @__PURE__ */ n("label", { children: [r, /* @__PURE__ */ n("select", {
					"aria-label": r,
					value: h[e] ?? "",
					onChange: (t) => g((n) => ({
						...n,
						[e]: t.target.value
					})),
					children: [/* @__PURE__ */ t("option", {
						value: "",
						children: "All"
					}), i.map((n) => /* @__PURE__ */ t("option", {
						value: n,
						children: e === "evidence" ? n === "none" ? "No recorded history" : `${n} · unverified import` : n
					}, n))]
				})] }, e)),
				/* @__PURE__ */ t("button", {
					onClick: () => g({}),
					children: "Reset filters"
				})
			]
		}),
		/* @__PURE__ */ n("p", {
			role: "status",
			children: [
				v.length,
				" of ",
				_.length,
				" versions"
			]
		}),
		v.length === 0 && /* @__PURE__ */ t("p", { children: "No strategies match." }),
		/* @__PURE__ */ t("div", {
			className: "wr-grid",
			children: v.map((e) => /* @__PURE__ */ n("article", {
				"aria-label": `${e.name} strategy`,
				children: [
					/* @__PURE__ */ t("h3", { children: e.name }),
					/* @__PURE__ */ t("code", {
						title: e.version.digest,
						children: e.version.digest.slice(0, 12)
					}),
					/* @__PURE__ */ n("dl", {
						className: "wr-meta",
						children: [
							/* @__PURE__ */ t("dt", { children: "Asset mint" }),
							/* @__PURE__ */ t("dd", { children: e.asset }),
							/* @__PURE__ */ t("dt", { children: "Thesis" }),
							/* @__PURE__ */ t("dd", { children: e.thesis }),
							/* @__PURE__ */ t("dt", { children: "Horizon" }),
							/* @__PURE__ */ n("dd", { children: [e.horizonMinutes, " min"] }),
							/* @__PURE__ */ t("dt", { children: "Source" }),
							/* @__PURE__ */ t("dd", { children: e.version.origin }),
							/* @__PURE__ */ t("dt", { children: "Terms" }),
							/* @__PURE__ */ t("dd", { children: e.document.market.termsHash }),
							/* @__PURE__ */ t("dt", { children: "History" }),
							/* @__PURE__ */ t("dd", { children: e.evidence.length ? `${e.evidence.join(", ")} · unverified import` : "No recorded history" }),
							/* @__PURE__ */ t("dt", { children: "Saved" }),
							/* @__PURE__ */ t("dd", { children: /* @__PURE__ */ t("time", {
								dateTime: e.version.createdAt,
								children: e.version.createdAt
							}) }),
							/* @__PURE__ */ t("dt", { children: "Weights A–E" }),
							/* @__PURE__ */ t("dd", { children: e.document.allocation.weightsBps.map((e) => `${e / 100}%`).join(" / ") })
						]
					}),
					/* @__PURE__ */ n("button", {
						onClick: () => c(e.version),
						children: ["Inspect ", e.name]
					}),
					/* @__PURE__ */ n("button", {
						onClick: () => l(e.version),
						"aria-pressed": s.includes(e.version.digest),
						children: ["Compare ", e.name]
					}),
					/* @__PURE__ */ n("button", {
						onClick: () => u(e.version),
						children: ["Export ", e.name]
					}),
					p === "Mine" && d && /* @__PURE__ */ n("button", {
						disabled: f,
						onClick: () => d(e.version),
						children: ["Discuss ", e.name]
					})
				]
			}, e.version.digest))
		})
	] })] });
}
//#endregion
//#region src/StrategyCompare.tsx
function la({ versions: r, history: i }) {
	let [a, s] = o({}), c = r.map((e) => {
		let t = i[a[e.digest]];
		return t?.rows.some((t) => t.strategyDigest === e.digest) ? t : void 0;
	}), l = c.length >= 2 && c.every(Boolean), u = l ? Wi(c) : null, d = r.map((e) => Q(e.raw)), f = l ? c.map((e, t) => Ui(e, r[t].digest)) : [], p = [
		["Weights A–E", ...d.map((e) => e.allocation.weightsBps.map((e) => `${e / 100}%`).join(" / "))],
		["Turnover cap (SOL)", ...d.map((e) => $(e.limits.totalTurnoverLamports))],
		["Max unsettled (SOL)", ...d.map((e) => $(e.limits.maxUnsettledLamports))],
		["Every nth round", ...d.map((e) => String(e.schedule.everyNthRound))],
		["Max rounds", ...d.map((e) => String(e.schedule.maxRoundsTotal))],
		["Horizon (minutes)", ...d.map((e) => String(e.research.horizonMinutes))]
	], m = (e, i) => /* @__PURE__ */ t("div", {
		className: "wr-table",
		role: "region",
		"aria-label": `${i} scroll area`,
		tabIndex: 0,
		children: /* @__PURE__ */ n("table", {
			"aria-label": i,
			children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [/* @__PURE__ */ t("th", { children: "Field" }), r.map((e) => /* @__PURE__ */ n("th", { children: [
				Q(e.raw).name,
				/* @__PURE__ */ t("br", {}),
				/* @__PURE__ */ t("code", {
					title: e.digest,
					children: e.digest.slice(0, 12)
				})
			] }, e.digest))] }) }), /* @__PURE__ */ t("tbody", { children: e.map(([e, ...i]) => /* @__PURE__ */ n("tr", { children: [/* @__PURE__ */ t("th", {
				scope: "row",
				children: e
			}), i.map((e, n) => /* @__PURE__ */ t("td", { children: e }, r[n].digest))] }, e)) })]
		})
	});
	return /* @__PURE__ */ n("div", {
		className: "wr-panel",
		children: [/* @__PURE__ */ t("h3", { children: "Version comparison" }), r.length < 2 ? /* @__PURE__ */ t("p", { children: "Select two to four library versions." }) : /* @__PURE__ */ n(e, { children: [
			r.map((e) => /* @__PURE__ */ n("label", { children: [
				Q(e.raw).name,
				" dataset",
				/* @__PURE__ */ n("select", {
					"aria-label": `${Q(e.raw).name} dataset`,
					value: a[e.digest] ?? "",
					onChange: (t) => s((n) => ({
						...n,
						[e.digest]: Number(t.target.value)
					})),
					children: [/* @__PURE__ */ t("option", {
						value: "",
						disabled: !0,
						children: "Select imported dataset"
					}), i.map((t, r) => t.rows.some((t) => t.strategyDigest === e.digest) ? /* @__PURE__ */ n("option", {
						value: r,
						children: [
							t.scope.dataset,
							" · ",
							t.scope.evidence,
							" · import ",
							r + 1
						]
					}, r) : null)]
				})
			] }, e.digest)),
			m(p, "Strategy settings"),
			/* @__PURE__ */ t("p", {
				role: "status",
				children: u ? `${u.reason}${u.differences.length ? `: ${u.differences.join(", ")}` : ""}` : "Performance unavailable: import history for each selected version."
			}),
			l && /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t("h3", { children: "Recorded results" }), m([
				...[
					"market",
					"terms",
					"evidence",
					"from",
					"to",
					"dataset",
					"valuation",
					"costs",
					"coverage"
				].map((e) => [e, ...c.map((t) => t.scope[e])]),
				["Verification", ...c.map(() => "Unverified import")],
				["Gaps", ...c.map((e) => e.gaps.join("; ") || "None declared")],
				["Scope initial bankroll (SOL)", ...c.map((e) => e.initialBankroll === null ? "Unavailable" : $(e.initialBankroll))],
				["Scope skipped rounds", ...c.map((e) => e.skipped === null ? "Unavailable" : String(e.skipped))],
				["Scope pending exposure (SOL)", ...c.map((e) => e.pendingExposure === null ? "Unavailable" : $(e.pendingExposure))],
				...[
					[
						"Finalized mining records",
						"entries",
						!1
					],
					[
						"Unsettled records",
						"unsettled",
						!1
					],
					[
						"Committed (SOL)",
						"committed",
						!0
					],
					[
						"Returned principal (SOL)",
						"returned",
						!0
					],
					[
						"Fees (SOL)",
						"fees",
						!0
					],
					[
						"Forfeitures (SOL)",
						"forfeitures",
						!0
					],
					[
						"User costs (SOL)",
						"userCosts",
						!0
					],
					[
						"BTC received (base units)",
						"btcReceived",
						!1
					],
					[
						"BTC pending (base units)",
						"btcPending",
						!1
					],
					[
						"SAT earned (base units)",
						"satEarned",
						!1
					],
					[
						"SAT claimed (base units)",
						"satClaimed",
						!1
					],
					[
						"SAT sold (base units)",
						"satSold",
						!1
					],
					[
						"BTC sale proceeds (SOL)",
						"btcSaleSOL",
						!0
					],
					[
						"SAT sale proceeds (SOL)",
						"satSaleSOL",
						!0
					],
					[
						"Net mining cash flow (SOL)",
						"netSOL",
						!0
					],
					[
						"Cash-flow drawdown (SOL)",
						"drawdown",
						!0
					]
				].map(([e, t, n]) => [e, ...c.map((e, r) => {
					let i = String(f[r][t]);
					return n ? $(i) : i;
				})]),
				["Net per committed turnover", ...c.map((e, t) => {
					let n = f[t].perTurnover;
					return n ? `${$(n.numerator)} SOL / ${$(n.denominator)} SOL turnover` : "Unavailable: zero committed turnover";
				})],
				["SAT acquisition cost", ...c.map((e, t) => {
					let n = f[t].costPerSAT;
					return n ? `${n.numerator} lamports / ${n.denominator} received SAT base units` : "Unavailable: no received SAT";
				})],
				["Other receipts (SOL; excluded)", ...c.map((e, t) => Object.entries(f[t].overlays).map(([e, t]) => `${e}: ${$(t)}`).join("; ") || "None recorded")],
				[
					"Marked value",
					"Unavailable: no market valuation supplied",
					...r.slice(1).map(() => "Unavailable: no market valuation supplied")
				]
			], "Descriptive recorded results")] }),
			/* @__PURE__ */ t("p", { children: "Financial replay unavailable: captured terms, observations and participation must be supplied together. Imported totals are descriptive evidence." })
		] })]
	});
}
//#endregion
//#region src/ResearchWorkspace.tsx
var ua = {
	revision: 0,
	versions: [],
	bundles: []
};
function da(e, t) {
	let n = URL.createObjectURL(new Blob([t], { type: "application/json" })), r = document.createElement("a");
	r.href = n, r.download = e, r.click(), setTimeout(() => URL.revokeObjectURL(n), 1e3);
}
function fa({ storage: i, readResearch: a, onDiscussResearch: s, onDiscussSaved: c }) {
	let [l, u] = o(ua), [d, f] = o([]), [p, m] = o("Library"), [h, g] = o(""), [_, v] = o("My strategy"), [y, b] = o(""), [x, S] = o([]), [C, w] = o(""), [T, E] = o(!1), [D, O] = o(0), [k, A] = o("");
	r(() => {
		let e = !0;
		return u(ua), S([]), g(""), b(""), A(""), O(0), w(""), i.load().then((t) => {
			e && u(t);
		}).catch((t) => {
			e && w(String(t.message));
		}), () => {
			e = !1;
		};
	}, [i]), r(() => {
		let e = !0;
		return Promise.all([...Object.entries(Yr), ["custom", li().weights]].map(async ([e, t]) => {
			let n = ui({
				...li(),
				preset: e,
				weights: t
			});
			n.name = e, n.description = "Built-in fixture template. Bind and review a current market before execution.";
			let r = JSON.stringify(n, null, 2);
			return {
				digest: await zi(r),
				raw: r,
				name: e,
				createdAt: "2026-10-22T00:00:00Z",
				origin: "built-in fixture"
			};
		})).then((t) => {
			e && f(t);
		}), () => {
			e = !1;
		};
	}, []);
	let ee = [...d, ...l.versions].filter((e, t, n) => n.findIndex((t) => t.digest === e.digest) === t), j = x.map((e) => ee.find((t) => t.digest === e)).filter(Boolean);
	async function M(e) {
		E(!0), w("");
		try {
			u(await e());
		} catch (e) {
			w(e.message);
		} finally {
			E(!1);
		}
	}
	let N = l.bundles[D];
	async function P(e) {
		if (c) {
			E(!0), w("");
			try {
				await c(e);
			} catch (e) {
				w(e.message);
			} finally {
				E(!1);
			}
		}
	}
	return /* @__PURE__ */ n("section", {
		className: "wt wr",
		"aria-label": "Strategy research workspace",
		children: [
			/* @__PURE__ */ n("header", {
				className: "wr-heading",
				children: [
					/* @__PURE__ */ t("h2", { children: "Research & strategies" }),
					/* @__PURE__ */ t("span", { children: i.label }),
					/* @__PURE__ */ t("span", {
						className: "wt-badge",
						children: "Execution disabled"
					})
				]
			}),
			/* @__PURE__ */ t("nav", {
				"aria-label": "Research sections",
				children: [
					"Research",
					"Library",
					"Compare",
					"History"
				].map((e) => /* @__PURE__ */ n("button", {
					"aria-pressed": p === e,
					onClick: () => m(e),
					children: [e, e === "Compare" ? ` (${x.length})` : ""]
				}, e))
			}),
			C && /* @__PURE__ */ t("p", {
				role: "alert",
				children: C
			}),
			p === "Research" && /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t(Yi, {
				read: a,
				onDiscuss: s
			}), /* @__PURE__ */ t(ia, {})] }),
			p === "Library" && /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t(ca, {
				library: l,
				builtins: d,
				selected: x,
				inspect: (e) => {
					g(e.raw), v(`${e.name} copy`);
				},
				compare: (e) => {
					w(""), x.includes(e.digest) ? S(x.filter((t) => t !== e.digest)) : x.length < 4 ? S([...x, e.digest]) : w("Compare at most four versions");
				},
				exportVersion: (e) => da(`${e.digest}.json`, e.raw),
				discuss: c ? (e) => void P({
					libraryRevision: l.revision,
					strategyDigest: e.digest
				}) : void 0,
				busy: T
			}), /* @__PURE__ */ n("div", {
				className: "wr-editor",
				children: [
					/* @__PURE__ */ n("label", { children: ["Version name", /* @__PURE__ */ t("input", {
						value: _,
						onChange: (e) => v(e.target.value)
					})] }),
					/* @__PURE__ */ n("label", { children: ["Strategy JSON", /* @__PURE__ */ t("textarea", {
						"aria-label": "Strategy JSON",
						value: h,
						onChange: (e) => g(e.target.value),
						spellCheck: !1
					})] }),
					/* @__PURE__ */ n("label", { children: ["Import strategy", /* @__PURE__ */ t("input", {
						type: "file",
						accept: ".json,application/json",
						onChange: async (e) => {
							let t = e.target.files?.[0];
							if (t) {
								if (t.size > 65536) {
									w("Strategy exceeds 64 KiB");
									return;
								}
								g(await t.text());
							}
						}
					})] }),
					/* @__PURE__ */ t("button", {
						disabled: T || !h,
						onClick: () => void M(() => i.save(l.revision, h)),
						children: "Save original version"
					}),
					/* @__PURE__ */ t("button", {
						disabled: T || !h,
						onClick: () => void M(() => i.save(l.revision, Bi(h, _))),
						children: "Save cloned version"
					}),
					/* @__PURE__ */ t("button", {
						disabled: T,
						onClick: () => void M(i.load),
						children: "Reload library"
					})
				]
			})] }),
			p === "Compare" && /* @__PURE__ */ t(la, {
				versions: j,
				history: l.bundles
			}),
			p === "History" && /* @__PURE__ */ n("div", {
				className: "wr-panel",
				children: [
					/* @__PURE__ */ t("h3", { children: "Imported history" }),
					/* @__PURE__ */ n("label", { children: ["History JSON", /* @__PURE__ */ t("textarea", {
						"aria-label": "History JSON",
						value: y,
						onChange: (e) => b(e.target.value),
						spellCheck: !1
					})] }),
					/* @__PURE__ */ n("label", { children: ["Import history", /* @__PURE__ */ t("input", {
						type: "file",
						accept: ".json,application/json",
						onChange: async (e) => {
							let t = e.target.files?.[0];
							if (t) {
								if (t.size > 65536) {
									w("History exceeds 64 KiB");
									return;
								}
								b(await t.text());
							}
						}
					})] }),
					/* @__PURE__ */ t("button", {
						disabled: T || !y,
						onClick: () => void M(() => i.importHistory(l.revision, y)),
						children: "Save history import"
					}),
					/* @__PURE__ */ n("label", { children: ["Dataset", /* @__PURE__ */ t("select", {
						value: D,
						onChange: (e) => {
							O(Number(e.target.value)), A("");
						},
						children: l.bundles.map((e, t) => /* @__PURE__ */ n("option", {
							value: t,
							children: [
								e.scope.dataset,
								" · ",
								e.scope.evidence,
								" · import ",
								t + 1
							]
						}, t))
					})] }),
					N ? /* @__PURE__ */ n(e, { children: [
						c && /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ n("label", { children: ["History context scope", /* @__PURE__ */ n("select", {
							"aria-label": "History context scope",
							value: k,
							onChange: (e) => A(e.target.value),
							children: [/* @__PURE__ */ t("option", {
								value: "",
								children: "All recorded strategies"
							}), l.versions.filter((e) => N.rows.some((t) => t.strategyDigest === e.digest)).map((e) => /* @__PURE__ */ n("option", {
								value: e.digest,
								children: [
									e.name,
									" · ",
									e.digest.slice(0, 12)
								]
							}, e.digest))]
						})] }), /* @__PURE__ */ t("button", {
							disabled: T,
							onClick: () => void P({
								libraryRevision: l.revision,
								bundleIndex: D,
								...k ? { strategyDigest: k } : {}
							}),
							children: "Discuss history"
						})] }),
						/* @__PURE__ */ n("p", { children: [
							N.source,
							" · UNVERIFIED · ",
							N.scope.from,
							" —",
							" ",
							N.scope.to
						] }),
						/* @__PURE__ */ n("p", { children: [
							"Coverage: ",
							N.scope.coverage,
							" · gaps:",
							" ",
							N.gaps.join(", ") || "None declared",
							" · valuation:",
							" ",
							N.scope.valuation,
							" · costs: ",
							N.scope.costs
						] }),
						/* @__PURE__ */ t("button", {
							onClick: () => da(`history-${D}.json`, JSON.stringify(N, null, 2)),
							children: "Export history"
						}),
						/* @__PURE__ */ t("div", {
							className: "wr-table",
							children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ t("tr", { children: [
								"Event",
								"Finality",
								"Category",
								"Committed SOL",
								"Returned SOL",
								"Fees SOL",
								"Deductions SOL",
								"BTC received / pending (base units)",
								"SAT earned / claimed / sold (base units)"
							].map((e) => /* @__PURE__ */ t("th", { children: e }, e)) }) }), /* @__PURE__ */ t("tbody", { children: N.rows.map((e, r) => /* @__PURE__ */ n("tr", { children: [
								/* @__PURE__ */ n("td", { children: [
									e.id,
									" / ",
									e.cursor
								] }),
								/* @__PURE__ */ t("td", { children: e.finality }),
								/* @__PURE__ */ t("td", { children: e.category }),
								/* @__PURE__ */ t("td", { children: $(e.committed) }),
								/* @__PURE__ */ t("td", { children: $(e.returned) }),
								/* @__PURE__ */ t("td", { children: $(e.fees) }),
								/* @__PURE__ */ t("td", { children: $(e.forfeitures) }),
								/* @__PURE__ */ n("td", { children: [
									e.btcReceived,
									" / ",
									e.btcPending
								] }),
								/* @__PURE__ */ n("td", { children: [
									e.satEarned,
									" / ",
									e.satClaimed,
									" / ",
									e.satSold
								] })
							] }, r)) })] })
						})
					] }) : /* @__PURE__ */ t("p", { children: "No imported history. Conditional previews are not earned rewards." })
				]
			})
		]
	});
}
//#endregion
//#region src/index.tsx
var pa = [
	"A",
	"B",
	"C",
	"D",
	"E"
], ma = [
	"< −0.20%",
	"−0.20 to < −0.05%",
	"−0.05 to < +0.05%",
	"+0.05 to < +0.20%",
	"≥ +0.20%"
], ha = {
	wide: "Wide",
	center: "Center",
	"trend-up": "Trend up",
	"trend-down": "Trend down",
	tails: "Tails",
	custom: "Custom"
}, ga = Array.from({ length: 66 }, (e, t) => 142 + Math.sin(t * .43) * 14 + Math.cos(t * .16) * 19 - t * 1.2 + (t > 40 ? -18 : 0));
function _a({ weights: e, compact: n = !1 }) {
	return /* @__PURE__ */ t("div", {
		className: `wt-shape ${n ? "compact" : ""}`,
		"aria-label": "Capital allocation shape",
		children: Array.from({ length: 65 }, (n, r) => {
			let i = Math.min(4, Math.floor(r / 13)), a = e[Math.max(0, i - 1)], o = e[Math.min(4, i + 1)], s = r % 13 / 13, c = e[i] * .7 + (s < .5 ? a : o) * .3;
			return /* @__PURE__ */ t("i", { style: {
				height: `${Math.max(3, c / 100)}%`,
				background: r < 32 ? "var(--wt-cyan)" : "var(--wt-violet)"
			} }, r);
		})
	});
}
function va() {
	return /* @__PURE__ */ n("svg", {
		className: "wt-pricechart",
		viewBox: "0 0 760 360",
		role: "img",
		"aria-label": "Illustrative BTC price candles. Fixture data, not a forecast.",
		children: [
			/* @__PURE__ */ t("defs", { children: /* @__PURE__ */ n("linearGradient", {
				id: "wt-area",
				x1: "0",
				y1: "0",
				x2: "0",
				y2: "1",
				children: [/* @__PURE__ */ t("stop", {
					stopColor: "#27C5DA",
					stopOpacity: ".10"
				}), /* @__PURE__ */ t("stop", {
					offset: "1",
					stopColor: "#27C5DA",
					stopOpacity: "0"
				})]
			}) }),
			[
				60,
				120,
				180,
				240,
				300
			].map((e, r) => /* @__PURE__ */ n("g", { children: [/* @__PURE__ */ t("line", {
				x1: "0",
				x2: "686",
				y1: e,
				y2: e,
				stroke: "#373447",
				strokeDasharray: "2 5"
			}), /* @__PURE__ */ t("text", {
				x: "698",
				y: e + 4,
				fill: "#B7B2C8",
				fontSize: "12",
				children: (100300 - r * 100).toLocaleString()
			})] }, e)),
			/* @__PURE__ */ t("path", {
				d: `M 12 310 ${ga.map((e, t) => `L ${12 + t * 10} ${e}`).join(" ")} L 662 310 Z`,
				fill: "url(#wt-area)"
			}),
			ga.map((e, r) => {
				let i = r % 4 != 0, a = 7 + r * 7 % 12;
				return /* @__PURE__ */ n("g", {
					stroke: i ? "#27C5DA" : "#FF647C",
					fill: i ? "#27C5DA" : "#FF647C",
					children: [
						/* @__PURE__ */ t("line", {
							x1: 12 + r * 10,
							x2: 12 + r * 10,
							y1: e - a - 6,
							y2: e + 9
						}),
						/* @__PURE__ */ t("rect", {
							x: 9 + r * 10,
							y: e - a,
							width: "6",
							height: a
						}),
						/* @__PURE__ */ t("rect", {
							x: 9 + r * 10,
							y: 333 - r * 13 % 35,
							width: "6",
							height: r * 13 % 35 + 3,
							opacity: ".3"
						})
					]
				}, r);
			}),
			/* @__PURE__ */ t("line", {
				x1: "0",
				x2: "686",
				y1: "70",
				y2: "70",
				stroke: "#27C5DA",
				strokeDasharray: "4 5"
			}),
			/* @__PURE__ */ t("rect", {
				x: "691",
				y: "60",
				width: "65",
				height: "22",
				rx: "3",
				fill: "#27C5DA"
			}),
			/* @__PURE__ */ t("text", {
				x: "697",
				y: "75",
				fill: "#15141C",
				fontSize: "12",
				children: "100,280"
			}),
			[
				"00:00",
				"01:00",
				"02:00",
				"03:00",
				"04:00"
			].map((e, n) => /* @__PURE__ */ t("text", {
				x: n * 150 + 14,
				y: "356",
				fill: "#B7B2C8",
				fontSize: "12",
				children: e
			}, e))
		]
	});
}
function ya({ brand: s, logoUrl: c, initialView: l = "discover", onViewChange: u, onOpenResearch: d, previewProvider: f, readProvider: p }) {
	let [m, h] = o(l), [g, _] = o({ ...si }), [v, y] = o("price"), [b, x] = o("Sessions"), [S, C] = o("Chart"), [w, T] = o(!1), [E, D] = o(!1), [O, k] = o(""), [A, ee] = o(!1), [j, M] = o("All"), [N, P] = o("1h"), [F, te] = o("bootstrap"), I = (e) => {
		te(e), D(!1);
	}, ne = a(null);
	r(() => h(l), [l]), r(() => {
		if (!E) return;
		let e = (e) => {
			if (e.key === "Escape") {
				D(!1);
				return;
			}
			if (e.key === "Tab") {
				let t = document.querySelector(".wt-modal")?.querySelectorAll("button:not([disabled]),input,select,[tabindex=\"0\"]");
				t?.length && (e.preventDefault(), t[0].focus());
			}
		};
		return document.addEventListener("keydown", e), () => {
			document.removeEventListener("keydown", e), ne.current?.focus();
		};
	}, [E]);
	let L = (e) => {
		h(e), u?.(e);
	}, [re, ie] = o({}), [ae, oe] = o({}), R = Object.values(ae)[0] ?? null, z = !R && fi(g), se = () => {
		ie({}), oe({});
	}, ce = i(() => mi(), []), B = i(() => {
		try {
			if (R) throw Error(R);
			return {
				amounts: ci(g),
				error: null
			};
		} catch (e) {
			return {
				amounts: null,
				error: g.amount || R ? e.message : null
			};
		}
	}, [g, R]), V = i(() => {
		try {
			return {
				value: di(g),
				error: null
			};
		} catch (e) {
			return {
				value: null,
				error: e.message
			};
		}
	}, [g]), le = (e, t) => {
		D(!1), _((n) => ({
			...n,
			[e]: t
		}));
	}, ue = (e) => {
		D(!1), se(), _((t) => ({
			...t,
			preset: e,
			weights: e === "custom" ? t.weights : [...Yr[e]]
		}));
	}, de = () => {
		let e = null;
		try {
			e = Y(g.amount);
		} catch {}
		d?.({
			source: "fixture",
			asset: "SAT",
			reference: "BTC-USD",
			view: v,
			preset: g.preset,
			weightsBps: [...g.weights],
			amountLamports: e,
			horizonHours: g.hours,
			execution: "disabled"
		});
	}, fe = /* @__PURE__ */ n("nav", {
		className: "wt-nav",
		children: [
			/* @__PURE__ */ n("button", {
				className: "wt-brand",
				onClick: () => L("discover"),
				children: [s === "WEN" && c ? /* @__PURE__ */ t("img", {
					src: c,
					alt: ""
				}) : null, s]
			}),
			/* @__PURE__ */ t("button", {
				className: m === "discover" ? "active" : "",
				onClick: () => L("discover"),
				children: "Discover"
			}),
			/* @__PURE__ */ t("button", {
				className: m === "terminal" ? "active" : "",
				onClick: () => L("terminal"),
				children: "Terminal"
			}),
			/* @__PURE__ */ t("span", { className: "wt-nav-spacer" }),
			/* @__PURE__ */ t("span", {
				className: "wt-badge",
				children: "Fixture workspace"
			}),
			/* @__PURE__ */ t("button", {
				disabled: !0,
				className: "wt-wallet",
				children: "Execution disabled"
			})
		]
	}), pe = ji(f, B.amounts && !R ? {
		phaseCase: F,
		amounts: B.amounts
	} : null);
	r(() => {
		f && pe.display?.status !== "ready" && D(!1);
	}, [f, pe.display?.status]);
	let [me, he] = o(null), ge = JSON.stringify({
		draft: g,
		phase: F,
		weightEdits: re,
		weightErrors: ae
	}), _e = a(ge);
	_e.current = ge;
	let ve = async (e) => {
		ne.current = e.currentTarget, he(null);
		let t = _e.current;
		try {
			if (ui(g), f && await pe.review(), t !== _e.current) throw Error("Draft changed during review. Review the updated session.");
			D(!0);
		} catch (e) {
			D(!1), he(e.message);
		}
	}, ye = /* @__PURE__ */ n("div", {
		className: "wt-schedule",
		"data-section": "schedule",
		children: [
			/* @__PURE__ */ n("div", {
				className: "wt-heading",
				children: [/* @__PURE__ */ t("h3", { children: "Session" }), /* @__PURE__ */ t("span", {
					className: "wt-tag",
					children: "Draft"
				})]
			}),
			/* @__PURE__ */ n("div", {
				className: "wt-grid2",
				children: [/* @__PURE__ */ n("label", { children: ["Horizon", /* @__PURE__ */ n("select", {
					value: g.hours,
					onChange: (e) => le("hours", Number(e.target.value)),
					children: [/* @__PURE__ */ t("option", {
						value: 4,
						children: "4 hours"
					}), /* @__PURE__ */ t("option", {
						value: 12,
						children: "12 hours"
					})]
				})] }), /* @__PURE__ */ n("label", { children: ["Participate", /* @__PURE__ */ n("select", {
					value: g.cadence,
					onChange: (e) => le("cadence", Number(e.target.value)),
					children: [
						/* @__PURE__ */ t("option", {
							value: 1,
							children: "Every round"
						}),
						/* @__PURE__ */ t("option", {
							value: 2,
							children: "Every 2nd"
						}),
						/* @__PURE__ */ t("option", {
							value: 3,
							children: "Every 3rd"
						}),
						/* @__PURE__ */ t("option", {
							value: 6,
							children: "Every 6th"
						})
					]
				})] })]
			}),
			/* @__PURE__ */ n("label", { children: ["Maximum rounds", /* @__PURE__ */ t("input", {
				inputMode: "numeric",
				placeholder: "Required",
				value: g.maxRounds,
				onChange: (e) => le("maxRounds", e.target.value)
			})] }),
			/* @__PURE__ */ t("div", {
				className: "wt-slot-strip",
				"aria-label": "Illustrative session slots",
				children: Array.from({ length: g.hours * 6 }, (e, n) => /* @__PURE__ */ t("i", {
					title: `Slot ${n}`,
					className: n % g.cadence === 0 && n / g.cadence < Number(g.maxRounds) ? "selected" : ""
				}, n))
			}),
			/* @__PURE__ */ t("div", {
				className: "wt-grid2",
				children: [
					["turnover", "Turnover cap"],
					["unsettled", "Unsettled cap"],
					["loss", "Gross loss cap"],
					["fees", "Total fee cap"],
					["perFee", "Fee / round"]
				].map(([e, r]) => /* @__PURE__ */ n("label", { children: [
					r,
					" · SOL",
					/* @__PURE__ */ t("input", {
						inputMode: "decimal",
						placeholder: "Required",
						value: g[e],
						onChange: (t) => le(e, t.target.value)
					})
				] }, e))
			}),
			/* @__PURE__ */ n("details", {
				className: "wt-condition-example",
				children: [
					/* @__PURE__ */ t("summary", { children: "Inspect conditional example" }),
					/* @__PURE__ */ t("span", {
						className: "wt-tag",
						children: "Fixture only · not applied"
					}),
					/* @__PURE__ */ t("ol", { children: ce.allocation.rules.map((e, r) => /* @__PURE__ */ n("li", { children: [
						/* @__PURE__ */ n("b", { children: [
							"Rule ",
							r + 1,
							" · ",
							ha[e.preset]
						] }),
						/* @__PURE__ */ n("span", { children: [
							"1h BTC return ",
							e.when[0].operator === "gte" ? "≥" : "≤",
							" ",
							Number(e.when[0].valueBps) / 100,
							"%"
						] }),
						/* @__PURE__ */ t("span", { children: e.weightsBps.map((e, t) => `${pa[t]} ${e / 100}%`).join(" · ") })
					] }, r)) }),
					/* @__PURE__ */ t("p", { children: "First matching rule applies. Missing or stale inputs skip the round. Otherwise use Wide. No market conditions are evaluated here." })
				]
			}),
			/* @__PURE__ */ n("dl", {
				className: "wt-facts",
				children: [
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Fixture start · UTC" }), /* @__PURE__ */ t("dd", { children: "22 Oct 2026 · 00:00" })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Conditions" }), /* @__PURE__ */ t("dd", { children: "None · fixed allocation" })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Runner" }), /* @__PURE__ */ t("dd", { children: "None · inactive" })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Selected rounds" }), /* @__PURE__ */ n("dd", { children: [
						V.value?.slots.length ?? "—",
						" / ",
						g.hours * 6
					] })] }),
					/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Committed turnover" }), /* @__PURE__ */ n("dd", { children: [V.value ? X(V.value.turnoverLamports) : "—", " SOL"] })] })
				]
			})
		]
	});
	return /* @__PURE__ */ n("div", {
		className: "wt",
		role: "region",
		"aria-label": "Shared mining terminal",
		"data-brand": s,
		"data-testid": "strategy-terminal",
		children: [
			fe,
			m === "discover" ? /* @__PURE__ */ n("section", {
				className: "wt-discovery",
				children: [
					/* @__PURE__ */ n("div", {
						className: "wt-discovery-head",
						children: [/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("span", {
							className: "wt-kicker",
							children: "ASSET MINING"
						}), /* @__PURE__ */ t("h1", { children: "Markets" })] }), /* @__PURE__ */ n("div", {
							className: "wt-market-status",
							children: [/* @__PURE__ */ t("i", {}), "Fixture catalogue · live markets unavailable"]
						})]
					}),
					/* @__PURE__ */ n("div", {
						className: "wt-discovery-tools",
						children: [/* @__PURE__ */ t("div", {
							className: "wt-tabs",
							children: ["All", "Favorites"].map((e) => /* @__PURE__ */ t("button", {
								className: j === e ? "active" : "",
								onClick: () => M(e),
								children: e
							}, e))
						}), /* @__PURE__ */ t("input", {
							"aria-label": "Search assets",
							placeholder: "Search assets",
							value: O,
							onChange: (e) => k(e.target.value)
						})]
					}),
					/* @__PURE__ */ t("div", {
						className: "wt-market-table wt-table-scroll",
						children: /* @__PURE__ */ n("table", { children: [/* @__PURE__ */ t("thead", { children: /* @__PURE__ */ n("tr", { children: [
							/* @__PURE__ */ t("th", { children: "Asset / reference" }),
							/* @__PURE__ */ t("th", { children: "Price path" }),
							/* @__PURE__ */ t("th", { children: "Reference price" }),
							/* @__PURE__ */ t("th", { children: "Final round capital" }),
							/* @__PURE__ */ t("th", { children: "SAT available" }),
							/* @__PURE__ */ t("th", { children: "Open activity" }),
							/* @__PURE__ */ t("th", {})
						] }) }), /* @__PURE__ */ t("tbody", { children: (!O || "satcoin sat btc".includes(O.toLowerCase())) && (j !== "Favorites" || A) ? /* @__PURE__ */ n("tr", { children: [
							/* @__PURE__ */ t("td", { children: /* @__PURE__ */ n("div", {
								className: "wt-asset-cell",
								children: [
									/* @__PURE__ */ t("button", {
										"aria-label": A ? "Remove Satcoin favorite" : "Favorite Satcoin",
										onClick: () => ee(!A),
										children: A ? "★" : "☆"
									}),
									/* @__PURE__ */ t("span", {
										className: "wt-coin",
										children: "S"
									}),
									/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("b", { children: "Satcoin" }), /* @__PURE__ */ t("small", { children: "SAT / BTC-USD · Fixture" })] })
								]
							}) }),
							/* @__PURE__ */ t("td", { children: /* @__PURE__ */ t("svg", {
								width: "130",
								height: "40",
								viewBox: "0 0 130 40",
								"aria-label": "Illustrative BTC path",
								children: /* @__PURE__ */ t("path", {
									d: "M1 32 L10 27 18 31 28 22 40 28 49 19 60 22 70 12 80 17 90 6 100 11 110 5 128 8",
									fill: "none",
									stroke: "#27C5DA",
									strokeWidth: "2"
								})
							}) }),
							/* @__PURE__ */ n("td", { children: ["$100,280 ", /* @__PURE__ */ t("small", { children: "Illustrative BTC" })] }),
							/* @__PURE__ */ t("td", { children: "Unavailable" }),
							/* @__PURE__ */ t("td", { children: "Unavailable" }),
							/* @__PURE__ */ t("td", { children: "Not connected" }),
							/* @__PURE__ */ t("td", { children: /* @__PURE__ */ t("button", {
								className: "wt-primary",
								onClick: () => L("terminal"),
								children: "Explore fixture ↗"
							}) })
						] }) : /* @__PURE__ */ t("tr", { children: /* @__PURE__ */ t("td", {
							colSpan: 7,
							className: "wt-empty",
							children: "No matching assets"
						}) }) })] })
					}),
					/* @__PURE__ */ n("div", {
						className: "wt-unavailable",
						children: [/* @__PURE__ */ t("span", { children: "Future assets" }), /* @__PURE__ */ t("span", { children: "Not available" })]
					})
				]
			}) : /* @__PURE__ */ n(e, { children: [
				/* @__PURE__ */ n("div", {
					className: "wt-marketbar",
					children: [
						/* @__PURE__ */ t("button", {
							onClick: () => T(!w),
							className: "wt-context-toggle",
							"aria-expanded": w,
							children: "Market context"
						}),
						/* @__PURE__ */ t("span", {
							className: "wt-coin",
							children: "S"
						}),
						/* @__PURE__ */ n("b", { children: ["Satcoin ", /* @__PURE__ */ t("span", {
							className: "wt-muted",
							children: "/ BTC-USD"
						})] }),
						/* @__PURE__ */ t("span", {
							className: "wt-tag",
							children: "10m rounds"
						}),
						/* @__PURE__ */ t("span", { className: "wt-nav-spacer" }),
						/* @__PURE__ */ t("span", {
							className: "wt-muted",
							children: "22 Oct 2026 · fixture"
						})
					]
				}),
				/* @__PURE__ */ t("div", {
					className: "wt-mobile-tabs",
					children: [
						"Chart",
						"Allocate",
						"Schedule",
						"Results"
					].map((e) => /* @__PURE__ */ t("button", {
						className: S === e ? "active" : "",
						onClick: () => C(e),
						children: e
					}, e))
				}),
				/* @__PURE__ */ n("section", {
					className: `wt-workspace mobile-${S.toLowerCase()}`,
					children: [
						/* @__PURE__ */ n("aside", {
							className: `wt-context ${w ? "open" : ""}`,
							children: [
								/* @__PURE__ */ n("div", {
									className: "wt-panel wt-context-top",
									children: [/* @__PURE__ */ n("div", {
										className: "wt-asset-cell",
										children: [/* @__PURE__ */ t("span", {
											className: "wt-coin large",
											children: "S"
										}), /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("h2", { children: "Satcoin" }), /* @__PURE__ */ t("span", {
											className: "wt-muted",
											children: "BTC-USD reference"
										})] })]
									}), /* @__PURE__ */ n("dl", {
										className: "wt-facts",
										children: [
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Round" }), /* @__PURE__ */ t("dd", { children: "Fixture #0" })] }),
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "State" }), /* @__PURE__ */ t("dd", { children: "Research only" })] }),
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "BTC price" }), /* @__PURE__ */ n("dd", { children: ["$100,280 ", /* @__PURE__ */ t("small", { children: "Illustrative" })] })] }),
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "SAT price" }), /* @__PURE__ */ t("dd", { children: "Unavailable" })] }),
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Live observations" }), /* @__PURE__ */ t("dd", { children: "Disconnected" })] })
										]
									})]
								}),
								/* @__PURE__ */ n("div", {
									className: "wt-panel wt-context-distribution",
									children: [
										/* @__PURE__ */ t("h3", { children: "Allocation" }),
										/* @__PURE__ */ t(_a, {
											weights: g.weights,
											compact: !0
										}),
										/* @__PURE__ */ t("div", {
											className: "wt-five-labels",
											children: pa.map((e) => /* @__PURE__ */ t("span", { children: e }, e))
										}),
										/* @__PURE__ */ n("dl", {
											className: "wt-facts",
											children: [
												/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Funded ranges" }), /* @__PURE__ */ n("dd", { children: [g.weights.filter((e) => e > 0).length, " / 5"] })] }),
												/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Round length" }), /* @__PURE__ */ t("dd", { children: "10 minutes" })] }),
												/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Minimum / entry" }), /* @__PURE__ */ t("dd", { children: "0.01 SOL" })] })
											]
										})
									]
								}),
								/* @__PURE__ */ n("div", {
									className: "wt-panel wt-context-bottom",
									children: [/* @__PURE__ */ t("h3", { children: "Account" }), /* @__PURE__ */ n("dl", {
										className: "wt-facts",
										children: [
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Wallet" }), /* @__PURE__ */ t("dd", { children: "Not connected" })] }),
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Claims" }), /* @__PURE__ */ t("dd", { children: "Unavailable" })] }),
											/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Pending capital" }), /* @__PURE__ */ t("dd", { children: "Unavailable" })] })
										]
									})]
								})
							]
						}),
						/* @__PURE__ */ n("section", {
							className: "wt-center",
							children: [/* @__PURE__ */ n("div", {
								className: "wt-panel wt-chart-panel",
								children: [
									/* @__PURE__ */ n("div", {
										className: "wt-tabs wt-chart-tabs",
										children: [[
											"price",
											"outlook",
											"conditional"
										].map((e) => /* @__PURE__ */ t("button", {
											className: v === e ? "active" : "",
											onClick: () => y(e),
											children: e === "conditional" ? "Conditional result" : e[0].toUpperCase() + e.slice(1)
										}, e)), d ? /* @__PURE__ */ t("button", {
											className: "wt-research",
											"aria-label": "Research",
											onClick: de,
											children: "Research ↗"
										}) : null]
									}),
									/* @__PURE__ */ n("div", {
										className: "wt-chart-meta",
										children: [/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("span", {
											className: "wt-muted",
											children: "BTC / USD"
										}), /* @__PURE__ */ n("strong", { children: ["100,280", /* @__PURE__ */ t("span", {
											className: "wt-chart-change",
											children: "+0.28%"
										})] })] }), /* @__PURE__ */ t("span", {
											className: "wt-tag",
											children: "Illustrative fixture"
										})]
									}),
									/* @__PURE__ */ n("div", {
										className: "wt-chart-toolbar",
										children: [
											/* @__PURE__ */ t("div", {
												className: "wt-tabs small",
												children: [
													"30m",
													"1h",
													"4h",
													"12h",
													"24h"
												].map((e) => /* @__PURE__ */ t("button", {
													className: N === e ? "active" : "",
													onClick: () => P(e),
													children: e
												}, e))
											}),
											/* @__PURE__ */ t("span", { className: "wt-nav-spacer" }),
											/* @__PURE__ */ n("span", {
												className: "wt-muted",
												children: [N, " view · illustrative"]
											})
										]
									}),
									v === "price" ? /* @__PURE__ */ n(e, { children: [/* @__PURE__ */ t(va, {}), /* @__PURE__ */ t("div", {
										className: "wt-published-ranges",
										children: ma.map((e, r) => /* @__PURE__ */ n("span", { children: [/* @__PURE__ */ t("b", { children: pa[r] }), e] }, e))
									})] }) : v === "outlook" ? /* @__PURE__ */ n("div", {
										className: "wt-outlook",
										children: [/* @__PURE__ */ t("div", {
											className: "wt-outline-bars",
											children: [
												12,
												22,
												34,
												23,
												9
											].map((e, r) => /* @__PURE__ */ n("div", { children: [
												/* @__PURE__ */ t("i", { style: { height: e * 5 } }),
												/* @__PURE__ */ t("b", { children: pa[r] }),
												/* @__PURE__ */ n("span", { children: [e, "%"] })
											] }, r))
										}), /* @__PURE__ */ t("span", {
											className: "wt-muted",
											children: "Illustrative model distribution · no forecast provider connected"
										})]
									}) : /* @__PURE__ */ n("div", {
										className: "wt-conditional",
										children: [/* @__PURE__ */ t(_i, {
											value: F,
											onChange: I
										}), /* @__PURE__ */ t(vi, {
											phase: F,
											matched: z,
											dynamic: pe.display,
											load: () => {
												_(li()), se(), D(!1);
											}
										})]
									})
								]
							}), /* @__PURE__ */ n("div", {
								className: "wt-panel wt-records",
								children: [/* @__PURE__ */ t("div", {
									className: "wt-tabs",
									children: [
										"Sessions",
										"Entries",
										"Claims",
										"History"
									].map((e) => /* @__PURE__ */ t("button", {
										className: b === e ? "active" : "",
										onClick: () => x(e),
										children: e
									}, e))
								}), b === "Sessions" ? /* @__PURE__ */ n("div", {
									className: "wt-session-row",
									children: [
										/* @__PURE__ */ t("span", { className: "wt-status-dot" }),
										/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ n("b", { children: [
											ha[g.preset],
											" · ",
											g.hours,
											"h"
										] }), /* @__PURE__ */ t("small", { children: "Local fixture draft · execution disabled" })] }),
										/* @__PURE__ */ t("span", { className: "wt-nav-spacer" }),
										/* @__PURE__ */ n("span", { children: [V.value?.slots.length ?? "—", " rounds"] }),
										/* @__PURE__ */ t("button", {
											onClick: () => {
												C("Schedule"), document.querySelector(".wt-schedule")?.scrollIntoView({ block: "nearest" });
											},
											children: "Inspect"
										})
									]
								}) : b === "Entries" ? /* @__PURE__ */ t(vi, {
									phase: F,
									matched: z,
									dynamic: pe.display
								}) : p ? /* @__PURE__ */ t(ki, { provider: p }) : /* @__PURE__ */ t(Si, {})]
							})]
						}),
						/* @__PURE__ */ n("aside", {
							className: "wt-ticket wt-panel",
							children: [
								/* @__PURE__ */ n("div", {
									className: "wt-ticket-head",
									children: [/* @__PURE__ */ t("h2", { children: "Build strategy" }), /* @__PURE__ */ t("button", {
										className: "wt-quiet",
										onClick: () => {
											_({ ...si }), se(), D(!1);
										},
										"aria-label": "Reset draft",
										children: "↺"
									})]
								}),
								/* @__PURE__ */ n("div", {
									className: "wt-allocation",
									children: [
										/* @__PURE__ */ n("div", {
											className: "wt-heading",
											children: [/* @__PURE__ */ t("h3", { children: "Amount / round" }), /* @__PURE__ */ t("button", {
												className: "wt-text-button",
												onClick: () => {
													_(li()), se(), D(!1);
												},
												children: "Load fixture"
											})]
										}),
										/* @__PURE__ */ n("div", {
											className: "wt-amount",
											children: [
												/* @__PURE__ */ t("span", {
													className: "wt-sol-symbol",
													children: "◎"
												}),
												/* @__PURE__ */ t("b", { children: "SOL" }),
												/* @__PURE__ */ t("input", {
													"aria-label": "SOL per round",
													inputMode: "decimal",
													placeholder: "0.00",
													value: g.amount,
													onChange: (e) => le("amount", e.target.value)
												})
											]
										}),
										/* @__PURE__ */ t("h3", { children: "Allocation shape" }),
										/* @__PURE__ */ t("div", {
											className: "wt-presets",
											children: Object.entries(ha).map(([e, n]) => /* @__PURE__ */ t("button", {
												className: g.preset === e ? "active" : "",
												onClick: () => ue(e),
												children: n
											}, e))
										}),
										/* @__PURE__ */ t(_a, { weights: g.weights }),
										/* @__PURE__ */ t("div", {
											className: "wt-five-labels",
											children: pa.map((e) => /* @__PURE__ */ t("span", { children: e }, e))
										}),
										/* @__PURE__ */ t("div", {
											className: "wt-weights",
											children: pa.map((e, r) => /* @__PURE__ */ n("label", { children: [/* @__PURE__ */ n("span", { children: [
												e,
												" ",
												/* @__PURE__ */ t("small", { children: ma[r] })
											] }), /* @__PURE__ */ n("div", { children: [
												/* @__PURE__ */ t("input", {
													"aria-label": `Range ${e} allocation percent`,
													type: "text",
													inputMode: "decimal",
													"aria-invalid": !!ae[r],
													value: re[r] ?? String(g.weights[r] / 100),
													onChange: (e) => {
														let t = e.target.value;
														ie((e) => ({
															...e,
															[r]: t
														})), D(!1);
														try {
															let e = pi(t);
															oe((e) => {
																let t = { ...e };
																return delete t[r], t;
															}), _((t) => {
																let n = [...t.weights];
																return n[r] = e, {
																	...t,
																	preset: "custom",
																	weights: n
																};
															});
														} catch (e) {
															oe((t) => ({
																...t,
																[r]: e.message
															}));
														}
													}
												}),
												/* @__PURE__ */ t("span", { children: "%" }),
												/* @__PURE__ */ n("b", { children: [B.amounts ? X(B.amounts[r]) : "—", " SOL"] })
											] })] }, e))
										}),
										/* @__PURE__ */ n("div", {
											className: `wt-weight-total ${g.weights.reduce((e, t) => e + t, 0) === 1e4 ? "" : "error"}`,
											children: [
												"Total ",
												(g.weights.reduce((e, t) => e + t, 0) / 100).toFixed(2),
												" / 100%"
											]
										}),
										B.error ? /* @__PURE__ */ t("p", {
											className: "wt-error",
											role: "alert",
											children: B.error
										}) : null
									]
								}),
								ye,
								/* @__PURE__ */ n("div", {
									className: "wt-review-footer",
									children: [
										/* @__PURE__ */ t("button", {
											className: "wt-primary",
											disabled: !V.value || !!R || !!f && pe.display?.status !== "ready",
											onClick: (e) => void ve(e),
											children: "Review fixture draft"
										}),
										/* @__PURE__ */ t("span", { children: "Execution disabled · no wallet authority" }),
										me ? /* @__PURE__ */ t("p", {
											role: "alert",
											children: me
										}) : null
									]
								})
							]
						})
					]
				})
			] }),
			E && V.value ? /* @__PURE__ */ t("div", {
				className: "wt-modal-shade",
				children: /* @__PURE__ */ n("section", {
					role: "dialog",
					"aria-modal": "true",
					"aria-labelledby": "wt-review-title",
					className: "wt-modal",
					children: [
						/* @__PURE__ */ n("div", {
							className: "wt-heading",
							children: [/* @__PURE__ */ t("h2", {
								id: "wt-review-title",
								children: "Review fixture draft"
							}), /* @__PURE__ */ t("button", {
								onClick: () => D(!1),
								autoFocus: !0,
								"aria-label": "Close review",
								children: "✕"
							})]
						}),
						/* @__PURE__ */ t("span", {
							className: "wt-badge",
							children: "Inactive · fixture data only"
						}),
						/* @__PURE__ */ n("dl", {
							className: "wt-facts",
							children: [
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Settlement fixture" }), /* @__PURE__ */ t("dd", { children: gi[F] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Allocation" }), /* @__PURE__ */ t("dd", { children: ha[g.preset] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Amount / round" }), /* @__PURE__ */ n("dd", { children: [g.amount, " SOL"] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Selected rounds" }), /* @__PURE__ */ t("dd", { children: V.value.slots.length })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Maximum turnover" }), /* @__PURE__ */ n("dd", { children: [X(V.value.turnoverLamports), " SOL"] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Unsettled cap" }), /* @__PURE__ */ n("dd", { children: [g.unsettled, " SOL"] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Gross loss cap" }), /* @__PURE__ */ n("dd", { children: [g.loss, " SOL"] })] }),
								/* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("dt", { children: "Runner / mandate" }), /* @__PURE__ */ t("dd", { children: "None" })] })
							]
						}),
						/* @__PURE__ */ t("div", {
							className: "wt-review-amounts",
							children: pa.map((e, r) => /* @__PURE__ */ n("div", { children: [/* @__PURE__ */ t("span", { children: e }), /* @__PURE__ */ n("b", { children: [X(B.amounts[r]), " SOL"] })] }, e))
						}),
						/* @__PURE__ */ t("button", {
							disabled: !0,
							className: "wt-primary",
							children: "Execution unavailable"
						})
					]
				})
			}) : null
		]
	});
}
//#endregion
export { fa as ResearchWorkspace, la as StrategyCompare, ya as StrategyTerminal, Gi as browserStorage, Bi as cloneStrategy, $r as compileAllocation, Jr as createMiningReadClient };

//# sourceMappingURL=index.js.map