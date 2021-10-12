// Package router inits bridges and loads onchain configs.
package router

import (
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/anyswap/CrossChain-Router/v3/common"
	"github.com/anyswap/CrossChain-Router/v3/log"
	"github.com/anyswap/CrossChain-Router/v3/params"
	"github.com/anyswap/CrossChain-Router/v3/tokens"
	mapset "github.com/deckarep/golang-set"
)

// router bridges
var (
	RouterBridges    = make(map[string]tokens.IBridge)    // key is chainID
	MultichainTokens = make(map[string]map[string]string) // key is tokenID,chainID
	AllChainIDs      []*big.Int                           // all chainIDs is retrieved only once
	AllTokenIDs      []string                             // all tokenIDs can be reload

	pausedChainIDs = mapset.NewSet() // paused chainIDs in memory by admin command

	MPCPublicKeys = make(map[string]string)          // key is mpc address
	RouterInfos   = make(map[string]*SwapRouterInfo) // key is router contract address

	IsIniting              bool
	RetryRPCCountInInit    = 10
	RetryRPCIntervalInInit = 1 * time.Second
)

// SwapRouterInfo swap router info
type SwapRouterInfo struct {
	RouterMPC     string
	RouterFactory string
	RouterWNative string
}

// GetBridgeByChainID get bridge by chain id
func GetBridgeByChainID(chainID string) tokens.IBridge {
	return RouterBridges[chainID]
}

// SetRouterInfo set router info
func SetRouterInfo(router string, routerInfo *SwapRouterInfo) {
	if router == "" {
		return
	}
	key := strings.ToLower(router)
	if _, exist := RouterInfos[key]; exist {
		return
	}
	RouterInfos[key] = routerInfo
}

// GetRouterInfo get router info
func GetRouterInfo(router string) *SwapRouterInfo {
	key := strings.ToLower(router)
	if info, exist := RouterInfos[key]; exist {
		return info
	}
	return nil
}

// GetTokenRouterContract get token router contract
func GetTokenRouterContract(tokenID, chainID string) (string, error) {
	bridge := GetBridgeByChainID(chainID)
	if bridge == nil {
		return "", tokens.ErrNoBridgeForChainID
	}
	multichainToken := ""
	if !tokens.IsAnyCallRouter() {
		multichainToken = GetCachedMultichainToken(tokenID, chainID)
		if multichainToken == "" {
			log.Warn("GetTokenRouterContract get multichain token failed", "tokenID", tokenID, "chainID", chainID)
			return "", tokens.ErrMissTokenConfig
		}
	}
	routerContract := bridge.GetRouterContract(multichainToken)
	if routerContract == "" {
		return "", tokens.ErrMissRouterInfo
	}
	return routerContract, nil
}

// GetTokenRouterInfo get token router info
func GetTokenRouterInfo(tokenID, chainID string) (*SwapRouterInfo, error) {
	routerContract, err := GetTokenRouterContract(tokenID, chainID)
	if err != nil {
		return nil, err
	}
	routerInfo := GetRouterInfo(routerContract)
	if routerInfo == nil {
		return nil, tokens.ErrMissRouterInfo
	}
	return routerInfo, nil
}

// GetRouterMPC get router mpc on dest chain (to build swapin tx)
func GetRouterMPC(tokenID, chainID string) (string, error) {
	routerInfo, err := GetTokenRouterInfo(tokenID, chainID)
	if err != nil {
		return "", err
	}
	return routerInfo.RouterMPC, nil
}

// SetMPCPublicKey set router mpc public key
func SetMPCPublicKey(mpc, pubkey string) {
	key := strings.ToLower(mpc)
	if _, exist := MPCPublicKeys[key]; exist {
		return
	}
	MPCPublicKeys[key] = pubkey
}

// GetMPCPublicKey get mpc puvlic key
func GetMPCPublicKey(mpc string) string {
	key := strings.ToLower(mpc)
	if pubkey, exist := MPCPublicKeys[key]; exist {
		return pubkey
	}
	return ""
}

// GetCachedMultichainTokens get multichain tokens of `tokenid`
func GetCachedMultichainTokens(tokenID string) map[string]string {
	tokenIDKey := strings.ToLower(tokenID)
	return MultichainTokens[tokenIDKey]
}

// GetCachedMultichainToken get multichain token address by tokenid and chainid
func GetCachedMultichainToken(tokenID, chainID string) (tokenAddr string) {
	tokenIDKey := strings.ToLower(tokenID)
	mcTokens := MultichainTokens[tokenIDKey]
	if mcTokens == nil {
		return ""
	}
	return mcTokens[chainID]
}

// PrintMultichainTokens print
func PrintMultichainTokens() {
	log.Info("*** begin print all multichain tokens")
	for tokenID, tokensMap := range MultichainTokens {
		log.Infof("*** multichain tokens of tokenID '%v' count is %v", tokenID, len(tokensMap))
		for chainID, tokenAddr := range tokensMap {
			log.Infof("*** multichain token: chainID %v tokenAddr %v", chainID, tokenAddr)
		}
	}
	log.Info("*** end print all multichain tokens")
}

// IsBigValueSwap is big value swap
func IsBigValueSwap(swapInfo *tokens.SwapTxInfo) bool {
	if swapInfo.SwapType != tokens.ERC20SwapType {
		return false
	}
	tokenID := swapInfo.GetTokenID()
	if params.IsInBigValueWhitelist(tokenID, swapInfo.From) ||
		params.IsInBigValueWhitelist(tokenID, swapInfo.TxTo) {
		return false
	}
	bridge := GetBridgeByChainID(swapInfo.FromChainID.String())
	if bridge == nil {
		return false
	}
	tokenCfg := bridge.GetTokenConfig(swapInfo.ERC20SwapInfo.Token)
	if tokenCfg == nil {
		return false
	}
	fromDecimals := tokenCfg.Decimals
	bigValueThreshold := tokens.GetBigValueThreshold(tokenID, swapInfo.FromChainID.String(), swapInfo.ToChainID.String(), fromDecimals)
	return swapInfo.Value.Cmp(bigValueThreshold) > 0
}

// IsBlacklistSwap is swap blacked
func IsBlacklistSwap(swapInfo *tokens.SwapTxInfo) bool {
	return params.IsChainIDInBlackList(swapInfo.FromChainID.String()) ||
		params.IsChainIDInBlackList(swapInfo.ToChainID.String()) ||
		params.IsTokenIDInBlackList(swapInfo.GetTokenID()) ||
		params.IsAccountInBlackList(swapInfo.From) ||
		params.IsAccountInBlackList(swapInfo.Bind) ||
		params.IsAccountInBlackList(swapInfo.TxTo)
}

// AddPausedChainIDs add paused chainIDs
func AddPausedChainIDs(chainIDs []string) {
	for _, chainID := range chainIDs {
		_, err := common.GetBigIntFromStr(chainID)
		if err != nil || chainID == "" {
			continue
		}
		pausedChainIDs.Add(chainID)
	}
}

// RemovePausedChainIDs remove paused chainIDs
func RemovePausedChainIDs(chainIDs []string) {
	for _, chainID := range chainIDs {
		_, err := common.GetBigIntFromStr(chainID)
		if err != nil || chainID == "" {
			continue
		}
		pausedChainIDs.Remove(chainID)
	}
}

// GetPausedChainIDs get paused chainIDs
func GetPausedChainIDs() []*big.Int {
	count := pausedChainIDs.Cardinality()
	if count == 0 {
		return nil
	}
	chainIDs := make([]*big.Int, 0, count)
	pausedChainIDs.Each(func(elem interface{}) bool {
		chainID, err := common.GetBigIntFromStr(elem.(string))
		if err == nil {
			chainIDs = append(chainIDs, chainID)
		}
		return false // stop iterate if return true
	})
	sort.Slice(chainIDs, func(i, j int) bool {
		return chainIDs[i].Cmp(chainIDs[j]) < 0
	})
	return chainIDs
}

// IsChainIDPaused is chainID paused
func IsChainIDPaused(chainID string) bool {
	return pausedChainIDs.Contains(chainID)
}
