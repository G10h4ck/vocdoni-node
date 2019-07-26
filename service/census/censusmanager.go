package censusmanager

import (
	"encoding/json"
	"net/http"
	"time"

	"gitlab.com/vocdoni/go-dvote/types"

	signature "gitlab.com/vocdoni/go-dvote/crypto/signature"
	"gitlab.com/vocdoni/go-dvote/log"
	tree "gitlab.com/vocdoni/go-dvote/tree"
)

// Time window (seconds) in which TimeStamp will be accepted if auth enabled
const authTimeWindow = 10

// MkTrees map of merkle trees indexed by censusId
var MkTrees map[string]*tree.Tree

// Signatures map of management pubKeys indexed by censusId
var Signatures map[string]string

var currentSignature signature.SignKeys

// AddNamespace adds a new merkletree identified by a censusId (name)
func AddNamespace(name, pubKey string) {
	if len(MkTrees) == 0 {
		MkTrees = make(map[string]*tree.Tree)
	}
	if len(Signatures) == 0 {
		Signatures = make(map[string]string)
	}
	log.Infof("adding namespace %s", name)
	mkTree := tree.Tree{}
	mkTree.Init(name)
	MkTrees[name] = &mkTree
	Signatures[name] = pubKey
}

func httpReply(resp *types.CensusResponseMessage, w http.ResponseWriter) {
	err := json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, err.Error(), 500)
	} else {
		w.Header().Set("content-type", "application/json")
	}
}

func checkRequest(w http.ResponseWriter, req *http.Request) bool {
	if req.Body == nil {
		http.Error(w, "Please send a request body", 400)
		return false
	}
	return true
}

func checkAuth(timestamp int32, signed, pubKey, message string) bool {
	if len(pubKey) < 1 {
		return true
	}
	currentTime := int32(time.Now().Unix())
	if timestamp < currentTime+authTimeWindow &&
		timestamp > currentTime-authTimeWindow {
		v, err := currentSignature.Verify(message, signed, pubKey)
		if err != nil {
			log.Warnf("Verification error: %s\n", err)
		}
		return v
	}
	return false
}

func HTTPhandler(w http.ResponseWriter, req *http.Request, signer *signature.SignKeys) {
	log.Debug("new request received")
	var rm types.CensusRequestMessage
	if ok := checkRequest(w, req); !ok {
		return
	}
	// Decode JSON
	log.Debug("Decoding JSON")

	/*
		buf := new(bytes.Buffer)
		buf.ReadFrom(req.Body)
		reqStr := buf.String()
		log.Debug(reqStr)
	*/
	err := json.NewDecoder(req.Body).Decode(&rm)
	if err != nil {
		log.Warnf("cannot decode JSON: %s", err.Error())
		http.Error(w, err.Error(), 400)
		return
	}
	if len(rm.Request.Method) < 1 {
		http.Error(w, "method must be specified", 400)
		return
	}
	log.Debugf("found method %s", rm.Request.Method)
	resp := Handler(&rm.Request, true)
	respMsg := new(types.CensusResponseMessage)
	respMsg.Response = *resp
	respMsg.ID = rm.ID
	respMsg.Response.Request = rm.ID
	respMsg.Signature, err = signer.SignJSON(respMsg.Response)
	if err != nil {
		log.Warn(err.Error())
	}
	httpReply(respMsg, w)
}

func Handler(r *types.CensusRequest, isAuth bool) *types.CensusResponse {
	resp := new(types.CensusResponse)
	op := r.Method
	var err error

	// Process data
	log.Infof("processing data => %+v", *r)

	resp.Ok = true
	resp.Error = ""
	resp.TimeStamp = int32(time.Now().Unix())
	censusFound := false
	for k := range MkTrees {
		if k == r.CensusID {
			censusFound = true
			break
		}
	}
	if !censusFound {
		resp.Ok = false
		resp.Error = "censusId not valid or not found"
		return resp
	}

	//Methods without rootHash
	if op == "getRoot" {
		resp.Root = MkTrees[r.CensusID].GetRoot()
		return resp
	}

	if op == "addClaim" {
		if isAuth {
			err = MkTrees[r.CensusID].AddClaim([]byte(r.ClaimData))
			if err != nil {
				log.Warnf("error adding claim: %s", err.Error())
				resp.Ok = false
				resp.Error = err.Error()
			} else {
				log.Info("claim addedd successfully ")
			}
		} else {
			resp.Ok = false
			resp.Error = "invalid authentication"
		}
		return resp
	}

	//Methods with rootHash, if rootHash specified snapshot the tree
	var t *tree.Tree
	if len(r.RootHash) > 1 { //if rootHash specified
		t, err = MkTrees[r.CensusID].Snapshot(r.RootHash)
		if err != nil {
			log.Warnf("snapshot error: %s", err.Error())
			resp.Ok = false
			resp.Error = "invalid root hash"
			return resp
		}
	} else { //if rootHash not specified use current tree
		t = MkTrees[r.CensusID]
	}

	if op == "genProof" {
		resp.Siblings, err = t.GenProof([]byte(r.ClaimData))
		if err != nil {
			resp.Ok = false
			resp.Error = err.Error()
		}
		return resp
	}

	if op == "getIdx" {
		resp.Idx, err = t.GetIndex([]byte(r.ClaimData))
		return resp
	}

	if op == "dump" {
		if !isAuth {
			resp.Ok = false
			resp.Error = "invalid authentication"
			return resp
		}
		//dump the claim data and return it
		values, err := t.Dump()
		if err != nil {
			resp.Ok = false
			resp.Error = err.Error()
		} else {
			resp.ClaimsData = values
		}
		return resp
	}

	if op == "checkProof" {
		if len(r.ProofData) < 1 {
			resp.Ok = false
			resp.Error = "proofData not provided"
			return resp
		}
		// Generate proof and return it
		validProof, err := t.CheckProof([]byte(r.ClaimData), r.ProofData)
		if err != nil {
			resp.Ok = false
			resp.Error = err.Error()
			return resp
		}
		if validProof {
			resp.ValidProof = true
		} else {
			resp.ValidProof = false
		}
		return resp
	}

	return resp
}
