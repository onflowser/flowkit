/*
 * Flow CLI
 *
 * Copyright 2019 Dapper Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/onflow/flowkit/v2"
	"github.com/onflow/flowkit/v2/accounts"
	"github.com/onflow/flowkit/v2/project"

	"path/filepath"

	"github.com/onflow/flow-go/fvm/systemcontracts"
	flowGo "github.com/onflow/flow-go/model/flow"

	"github.com/onflow/flowkit/v2/gateway"

	flowsdk "github.com/onflow/flow-go-sdk"

	"github.com/onflow/flowkit/v2/config"

	"github.com/onflow/flowkit/v2/output"
)

type categorizedLogs struct {
	fileSystemActions []string
	stateUpdates      []string
	issues            []string
}

func (cl *categorizedLogs) logAll(logger output.Logger) {
	logger.Info(output.MessageWithEmojiPrefix("📝", "Dependency Manager Actions Summary"))
	logger.Info("") // Add a line break after the section

	if len(cl.fileSystemActions) > 0 {
		logger.Info(output.MessageWithEmojiPrefix("🗃️", "File System Actions:"))
		for _, msg := range cl.fileSystemActions {
			logger.Info(msg)
		}
		logger.Info("") // Add a line break after the section
	}

	if len(cl.stateUpdates) > 0 {
		logger.Info(output.MessageWithEmojiPrefix("💾", "State Updates:"))
		for _, msg := range cl.stateUpdates {
			logger.Info(msg)
		}
		logger.Info("") // Add a line break after the section
	}

	if len(cl.issues) > 0 {
		logger.Info(output.MessageWithEmojiPrefix("⚠️", "Issues:"))
		for _, msg := range cl.issues {
			logger.Info(msg)
		}
		logger.Info("")
	}

	if len(cl.fileSystemActions) == 0 && len(cl.stateUpdates) == 0 {
		logger.Info(output.MessageWithEmojiPrefix("👍", "Zero changes were made. Everything looks good."))
	}
}

type DependencyInstaller struct {
	Gateways        map[string]gateway.Gateway
	Logger          output.Logger
	State           *flowkit.State
	SaveState       bool
	TargetDir       string
	SkipDeployments bool
	SkipAlias       bool
	logs            categorizedLogs
	dependencies    map[string]config.Dependency
	prompter        Prompter
}

type DeploymentData struct {
	Network   string
	Account   string
	Contracts []string
}

type Prompter interface {
	// ShouldUpdateDependency prompts the user if the dependency should be updated in flow configuration
	ShouldUpdateDependency(contractName string) bool
	// AddContractToDeployment prompts the user to select an account to deploy a given contract on a given network
	AddContractToDeployment(networkName string, accounts accounts.Accounts, contractName string) *DeploymentData
	// AddressPromptOrEmpty prompts the user to enter a flow address
	AddressPromptOrEmpty(label string, validate InputValidator) string
}

type InputValidator func(string) error

type Flags struct {
	SkipDeployments bool
	SkipAlias       bool
}

type Option func(*DependencyInstaller)

func WithLogger(logger output.Logger) Option {
	return func(installer *DependencyInstaller) {
		installer.Logger = logger
	}
}

func WithSaveState() Option {
	return func(installer *DependencyInstaller) {
		installer.SaveState = true
	}
}

func WithTargetDir(targetDir string) Option {
	return func(installer *DependencyInstaller) {
		installer.TargetDir = targetDir
	}
}

func WithSkippedDeployments() Option {
	return func(installer *DependencyInstaller) {
		installer.SkipDeployments = true
	}
}

func WithSkippedAlias() Option {
	return func(installer *DependencyInstaller) {
		installer.SkipAlias = true
	}
}

func WithGateways(gateways map[string]gateway.Gateway) Option {
	return func(installer *DependencyInstaller) {
		installer.Gateways = gateways
	}
}

// NewDependencyInstaller creates a new instance of DependencyInstaller
func NewDependencyInstaller(state *flowkit.State, prompter Prompter, options ...Option) (*DependencyInstaller, error) {
	installer := &DependencyInstaller{
		Gateways:        nil,
		Logger:          output.NewStdoutLogger(output.InfoLog),
		State:           state,
		SaveState:       false,
		TargetDir:       "",
		SkipDeployments: false,
		SkipAlias:       false,
		dependencies:    make(map[string]config.Dependency),
		prompter:        prompter,
	}

	for _, opt := range options {
		opt(installer)
	}

	// If custom gateway option wasn't provided, use default gateways
	if installer.Gateways == nil {
		gateways, err := defaultGateways()
		if err != nil {
			return nil, err
		}
		installer.Gateways = gateways
	}

	return installer, nil
}

func defaultGateways() (map[string]gateway.Gateway, error) {
	emulatorGateway, err := gateway.NewGrpcGateway(config.EmulatorNetwork)
	if err != nil {
		return nil, fmt.Errorf("error creating emulator gateway: %v", err)
	}

	testnetGateway, err := gateway.NewGrpcGateway(config.TestnetNetwork)
	if err != nil {
		return nil, fmt.Errorf("error creating testnet gateway: %v", err)
	}

	mainnetGateway, err := gateway.NewGrpcGateway(config.MainnetNetwork)
	if err != nil {
		return nil, fmt.Errorf("error creating mainnet gateway: %v", err)
	}

	previewnetGateway, err := gateway.NewGrpcGateway(config.PreviewnetNetwork)
	if err != nil {
		return nil, fmt.Errorf("error creating previewnet gateway: %v", err)
	}

	return map[string]gateway.Gateway{
		config.EmulatorNetwork.Name:   emulatorGateway,
		config.TestnetNetwork.Name:    testnetGateway,
		config.MainnetNetwork.Name:    mainnetGateway,
		config.PreviewnetNetwork.Name: previewnetGateway,
	}, nil
}

// saveState checks the SaveState flag and saves the state if set to true.
func (di *DependencyInstaller) saveState() error {
	if di.SaveState {
		statePath := filepath.Join(di.TargetDir, "flow.json")
		if err := di.State.Save(statePath); err != nil {
			return fmt.Errorf("error saving state: %w", err)
		}
	}
	return nil
}

// Install processes all the dependencies in the state and installs them and any dependencies they have
func (di *DependencyInstaller) Install() error {
	for _, dependency := range *di.State.Dependencies() {
		if err := di.processDependency(dependency); err != nil {
			di.Logger.Error(fmt.Sprintf("Error processing dependency: %v", err))
			return err
		}
	}

	di.checkForConflictingContracts()

	if err := di.saveState(); err != nil {
		return fmt.Errorf("error saving state: %w", err)
	}

	di.logs.logAll(di.Logger)

	return nil
}

// AddBySourceString processes a single dependency and installs it and any dependencies it has, as well as adding it to the state
func (di *DependencyInstaller) AddBySourceString(depSource, customName string) error {
	depNetwork, depAddress, depContractName, err := config.ParseSourceString(depSource)
	if err != nil {
		return fmt.Errorf("error parsing source: %w", err)
	}

	name := depContractName

	if customName != "" {
		name = customName
	}

	dep := config.Dependency{
		Name: name,
		Source: config.Source{
			NetworkName:  depNetwork,
			Address:      flowsdk.HexToAddress(depAddress),
			ContractName: depContractName,
		},
	}

	if err := di.processDependency(dep); err != nil {
		return fmt.Errorf("error processing dependency: %w", err)
	}

	di.checkForConflictingContracts()

	if err := di.saveState(); err != nil {
		return err
	}

	di.logs.logAll(di.Logger)

	return nil
}

// Add processes a single dependency and installs it and any dependencies it has, as well as adding it to the state
func (di *DependencyInstaller) Add(dep config.Dependency) error {
	if err := di.processDependency(dep); err != nil {
		return fmt.Errorf("error processing dependency: %w", err)
	}

	if err := di.saveState(); err != nil {
		return err
	}

	di.logs.logAll(di.Logger)

	return nil
}

// AddMany processes multiple dependencies and installs them as well as adding them to the state
func (di *DependencyInstaller) AddMany(dependencies []config.Dependency) error {
	for _, dep := range dependencies {
		if err := di.processDependency(dep); err != nil {
			return fmt.Errorf("error processing dependency: %w", err)
		}
	}

	if err := di.saveState(); err != nil {
		return err
	}

	di.logs.logAll(di.Logger)

	return nil
}

func (di *DependencyInstaller) addDependency(dep config.Dependency) error {
	sourceString := fmt.Sprintf("%s://%s.%s", dep.Source.NetworkName, dep.Source.Address.String(), dep.Source.ContractName)

	if _, exists := di.dependencies[sourceString]; exists {
		return nil
	}

	di.dependencies[sourceString] = dep

	return nil
}

// checkForConflictingContracts checks if any of the dependencies conflict with contracts already in the state
func (di *DependencyInstaller) checkForConflictingContracts() {
	for _, dependency := range di.dependencies {
		foundContract, _ := di.State.Contracts().ByName(dependency.Name)
		if foundContract != nil && !foundContract.IsDependency {
			msg := output.MessageWithEmojiPrefix("❌", fmt.Sprintf("Contract named %s already exists in flow.json", dependency.Name))
			di.logs.issues = append(di.logs.issues, msg)
		}
	}
}

func (di *DependencyInstaller) processDependency(dependency config.Dependency) error {
	depAddress := flowsdk.HexToAddress(dependency.Source.Address.String())
	return di.fetchDependencies(dependency.Source.NetworkName, depAddress, dependency.Name, dependency.Source.ContractName)
}

func (di *DependencyInstaller) fetchDependencies(networkName string, address flowsdk.Address, assignedName, contractName string) error {
	err := di.addDependency(config.Dependency{
		Name: assignedName,
		Source: config.Source{
			NetworkName:  networkName,
			Address:      address,
			ContractName: contractName,
		},
	})
	if err != nil {
		return fmt.Errorf("error adding dependency: %w", err)
	}

	ctx := context.Background()
	account, err := di.Gateways[networkName].GetAccount(ctx, address)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	if account == nil {
		return fmt.Errorf("account is nil for address: %s", address)
	}

	if account.Contracts == nil {
		return fmt.Errorf("contracts are nil for account: %s", address)
	}

	found := false

	for _, contract := range account.Contracts {
		program, err := project.NewProgram(contract, nil, "")
		if err != nil {
			return fmt.Errorf("failed to parse program: %w", err)
		}

		parsedContractName, err := program.Name()
		if err != nil {
			return fmt.Errorf("failed to parse contract name: %w", err)
		}

		if parsedContractName == contractName {
			found = true

			if err := di.handleFoundContract(networkName, address.String(), assignedName, parsedContractName, program); err != nil {
				return fmt.Errorf("failed to handle found contract: %w", err)
			}

			if program.HasAddressImports() {
				imports := program.AddressImportDeclarations()
				for _, imp := range imports {
					contractName := imp.Identifiers[0].String()
					err := di.fetchDependencies(networkName, flowsdk.HexToAddress(imp.Location.String()), contractName, contractName)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	if !found {
		errMsg := fmt.Sprintf("contract %s not found for account %s on network %s", contractName, address, networkName)
		di.Logger.Error(errMsg)
	}

	return nil
}

func (di *DependencyInstaller) contractFileExists(address, contractName string) bool {
	fileName := fmt.Sprintf("%s.cdc", contractName)
	path := filepath.Join("imports", address, fileName)

	_, err := di.State.ReaderWriter().Stat(path)

	return err == nil
}

func (di *DependencyInstaller) createContractFile(address, contractName, data string) error {
	fileName := fmt.Sprintf("%s.cdc", contractName)
	path := filepath.Join(di.TargetDir, "imports", address, fileName)
	dir := filepath.Dir(path)

	if err := di.State.ReaderWriter().MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating directories: %w", err)
	}

	if err := di.State.ReaderWriter().WriteFile(path, []byte(data), 0644); err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}

func (di *DependencyInstaller) handleFileSystem(contractAddr, contractName, contractData, networkName string) error {
	if !di.contractFileExists(contractAddr, contractName) {
		if err := di.createContractFile(contractAddr, contractName, contractData); err != nil {
			return fmt.Errorf("failed to create contract file: %w", err)
		}

		msg := output.MessageWithEmojiPrefix("✅️", fmt.Sprintf("Contract %s from %s on %s installed", contractName, contractAddr, networkName))
		di.logs.fileSystemActions = append(di.logs.fileSystemActions, msg)
	}

	return nil
}

func isCoreContract(contractName string) bool {
	sc := systemcontracts.SystemContractsForChain(flowGo.Emulator)

	for _, coreContract := range sc.All() {
		if coreContract.Name == contractName {
			return true
		}
	}
	return false
}

func (di *DependencyInstaller) handleFoundContract(networkName, contractAddr, assignedName, contractName string, program *project.Program) error {
	hash := sha256.New()
	hash.Write(program.CodeWithUnprocessedImports())
	originalContractDataHash := hex.EncodeToString(hash.Sum(nil))

	program.ConvertAddressImports()
	contractData := string(program.CodeWithUnprocessedImports())

	dependency := di.State.Dependencies().ByName(assignedName)

	// If a dependency by this name already exists and its remote source network or address does not match, then give option to stop or continue
	if dependency != nil && (dependency.Source.NetworkName != networkName || dependency.Source.Address.String() != contractAddr) {
		return fmt.Errorf("dependency named %s already exists with a different remote source, please fix the conflict and retry", assignedName)
	}

	// Check if remote source version is different from local version
	// If it is, ask if they want to update
	// If no hash, ignore
	if dependency != nil && dependency.Hash != "" && dependency.Hash != originalContractDataHash {
		if !di.prompter.ShouldUpdateDependency(contractName) {
			return nil
		}
	}

	// Needs to happen before handleFileSystem
	if !di.contractFileExists(contractAddr, contractName) {
		err := di.handleAdditionalDependencyTasks(networkName, contractName)
		if err != nil {
			di.Logger.Error(fmt.Sprintf("Error handling additional dependency tasks: %v", err))
			return err
		}
	}

	err := di.handleFileSystem(contractAddr, contractName, contractData, networkName)
	if err != nil {
		return fmt.Errorf("error handling file system: %w", err)
	}

	err = di.updateDependencyState(networkName, contractAddr, assignedName, contractName, originalContractDataHash)
	if err != nil {
		di.Logger.Error(fmt.Sprintf("Error updating state: %v", err))
		return err
	}

	return nil
}

func (di *DependencyInstaller) handleAdditionalDependencyTasks(networkName, contractName string) error {
	// If the contract is not a core contract and the user does not want to skip deployments, then prompt for a deployment
	if !di.SkipDeployments && !isCoreContract(contractName) {
		err := di.updateDependencyDeployment(contractName)
		if err != nil {
			di.Logger.Error(fmt.Sprintf("Error updating deployment: %v", err))
			return err
		}

		msg := output.MessageWithEmojiPrefix("✅", fmt.Sprintf("%s added to emulator deployments", contractName))
		di.logs.stateUpdates = append(di.logs.stateUpdates, msg)
	}

	// If the contract is not a core contract and the user does not want to skip aliasing, then prompt for an alias
	if !di.SkipAlias && !isCoreContract(contractName) {
		err := di.updateDependencyAlias(contractName, networkName)
		if err != nil {
			di.Logger.Error(fmt.Sprintf("Error updating alias: %v", err))
			return err
		}

		msg := output.MessageWithEmojiPrefix("✅", fmt.Sprintf("Alias added for %s on %s", contractName, networkName))
		di.logs.stateUpdates = append(di.logs.stateUpdates, msg)
	}

	return nil
}

func (di *DependencyInstaller) updateDependencyDeployment(contractName string) error {
	// Add to deployments
	// If a deployment already exists for that account, contract, and network, then ignore
	raw := di.prompter.AddContractToDeployment(config.EmulatorNetwork.Name, *di.State.Accounts(), contractName)

	if raw != nil {
		deployment := di.State.Deployments().ByAccountAndNetwork(raw.Account, raw.Network)
		if deployment == nil {
			di.State.Deployments().AddOrUpdate(config.Deployment{
				Network: raw.Network,
				Account: raw.Account,
			})
			deployment = di.State.Deployments().ByAccountAndNetwork(raw.Account, raw.Network)
		}

		for _, c := range raw.Contracts {
			deployment.AddContract(config.ContractDeployment{Name: c})
		}
	}

	return nil
}

func (di *DependencyInstaller) updateDependencyAlias(contractName, aliasNetwork string) error {
	var missingNetwork string

	if aliasNetwork == config.TestnetNetwork.Name {
		missingNetwork = config.MainnetNetwork.Name
	} else {
		missingNetwork = config.TestnetNetwork.Name
	}

	label := fmt.Sprintf("Enter an alias address for %s on %s if you have one, otherwise leave blank", contractName, missingNetwork)
	raw := di.prompter.AddressPromptOrEmpty(label, addressValidator)

	if raw != "" {
		contract, err := di.State.Contracts().ByName(contractName)
		if err != nil {
			return err
		}

		contract.Aliases.Add(missingNetwork, flowsdk.HexToAddress(raw))
	}

	return nil
}

func addressValidator(address string) error {
	if address == "" {
		return nil
	}
	if flowsdk.HexToAddress(address) == flowsdk.EmptyAddress {
		return fmt.Errorf("invalid alias address")
	}
	return nil
}

func (di *DependencyInstaller) updateDependencyState(networkName, contractAddress, assignedName, contractName, contractHash string) error {
	dep := config.Dependency{
		Name: assignedName,
		Source: config.Source{
			NetworkName:  networkName,
			Address:      flowsdk.HexToAddress(contractAddress),
			ContractName: contractName,
		},
		Hash: contractHash,
	}

	isNewDep := di.State.Dependencies().ByName(dep.Name) == nil

	di.State.Dependencies().AddOrUpdate(dep)
	di.State.Contracts().AddDependencyAsContract(dep, networkName)

	if isNewDep {
		msg := output.MessageWithEmojiPrefix("✅", fmt.Sprintf("%s added to flow.json", dep.Name))
		di.logs.stateUpdates = append(di.logs.stateUpdates, msg)
	}

	return nil
}
